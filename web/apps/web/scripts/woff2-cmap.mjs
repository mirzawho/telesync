// Reads the character→glyph map (cmap) out of a WOFF2 font without a browser
// or a font library.
//
// The workspace ships Vazirmatn as WOFF2 only, and the responsive tests need
// to know which characters the bundled face actually draws: a font that
// garbles Persian in production renders whole cards and table headers as a
// stream of boxes, and no class list or DOM check would see that. This reads
// the Brotli-compressed font table directory, decompresses the tables the
// workspace's brotli (a dependency of the Vite build) exposes, and parses
// only the `cmap` table's format-4 and format-12 subtables.
import { brotliDecompressSync } from "node:zlib"

const KNOWN_TABLE_TAGS = {
  0: "cmap",
  1: "head",
  2: "hhea",
  3: "hmtx",
  4: "maxp",
  5: "name",
  6: "OS/2",
  7: "post",
  8: "cvt ",
  9: "fpgm",
  10: "glyf",
  11: "loca",
  12: "prep",
  13: "CFF ",
  14: "VORG",
  15: "EBDT",
  16: "EBLC",
  17: "gasp",
  18: "hdmx",
  19: "kern",
  20: "LTSH",
  21: "PCLT",
  22: "VDMX",
  23: "vhea",
  24: "vmtx",
  25: "BASE",
  26: "GDEF",
  27: "GPOS",
  28: "GSUB",
  29: "EBSC",
  30: "JSTF",
  31: "MATH",
  32: "CBDT",
  33: "CBLC",
  34: "COLR",
  35: "CPAL",
  36: "SVG ",
  37: "sbix",
  38: "acnt",
  39: "avar",
  40: "bdat",
  41: "bloc",
  42: "bsln",
  43: "cvar",
  44: "fdsc",
  45: "feat",
  46: "fmtx",
  47: "fvar",
  48: "gvar",
  49: "hsty",
  50: "just",
  51: "lcar",
  52: "mort",
  53: "morx",
  54: "opbd",
  55: "prop",
  56: "trak",
  57: "Zapf",
  58: "Silf",
  59: "Glat",
  60: "Gloc",
  61: "Feat",
  62: "Sill",
}

function readBase128(buf, state) {
  let value = 0
  for (let i = 0; i < 5; i++) {
    const byte = buf[state.pos++]
    if (i === 0 && byte === 0x80) throw new Error("UIntBase128 leading zero")
    if (value & 0xfe000000) throw new Error("UIntBase128 overflow")
    value = (value << 7) | (byte & 0x7f)
    if ((byte & 0x80) === 0) return value >>> 0
  }
  throw new Error("UIntBase128 too long")
}

// parseWoff2Tables returns a Map of tag → table bytes (uncompressed) for a
// single-face WOFF2 file.
export function parseWoff2Tables(buf) {
  if (buf.toString("latin1", 0, 4) !== "wOF2") {
    throw new Error("not a WOFF2 file")
  }
  let pos = 4
  const flavor = buf.readUInt32BE(pos)
  pos += 4
  if (flavor === 0x74746366 /* 'ttcf' */) {
    throw new Error("WOFF2 collection not supported")
  }
  // reserved(2) numTables(2) totalSfntSize(4) totalCompressedSize(4): the
  // directory follows the 48-byte header. (majorVersion, minorVersion, the
  // meta block and the private block sit *after* the compressed stream, so
  // they are not part of it.)
  const numTables = buf.readUInt16BE(pos + 2)
  pos += 4 + 4 + 4

  const state = { pos }
  const entries = []
  for (let i = 0; i < numTables; i++) {
    const flags = buf[state.pos++]
    const index = flags & 0x3f
    const transform = (flags >> 6) & 0x03
    let tag
    if (index === 0x3f) {
      tag = buf.toString("latin1", state.pos, state.pos + 4)
      state.pos += 4
    } else {
      tag = KNOWN_TABLE_TAGS[index]
      if (!tag) throw new Error(`unknown WOFF2 table index ${index}`)
    }
    const origLength = readBase128(buf, state)
    // A transformed glyf/loca entry carries its transform version in the
    // transform bits instead of a transformLength; neither table is needed
    // for the character map, so only its length is tracked.
    if (tag === "glyf" || tag === "loca") {
      if (transform === 3) state.pos++
      entries.push({ tag, transformLength: origLength })
      continue
    }
    const transformLength = origLength
    entries.push({ tag, transformLength })
  }

  const compressedLength = buf.readUInt32BE(state.pos)
  if (compressedLength === 0) {
    throw new Error("uncompressed WOFF2 not supported")
  }
  const streamStart = state.pos + 4
  if (streamStart + compressedLength > buf.length) {
    throw new Error(`WOFF2 stream overruns file (${streamStart}+${compressedLength} > ${buf.length})`)
  }
  if (compressedLength === 0) {
    throw new Error("uncompressed WOFF2 not supported")
  }
  const stream = brotliDecompressSync(buf.subarray(streamStart, streamStart + compressedLength))

  const tables = new Map()
  let at = 0
  for (const entry of entries) {
    tables.set(entry.tag, stream.subarray(at, at + entry.transformLength))
    at += entry.transformLength
  }
  return tables
}

// glyphsOf returns the set of code points a font's cmap maps to a glyph.
export function glyphsOf(tables) {
  const cmap = tables.get("cmap")
  if (!cmap) throw new Error("font has no cmap table")
  const glyphs = new Set()
  const count = cmap.readUInt16BE(2)
  let seen4 = false
  for (let i = 0; i < count; i++) {
    const platform = cmap.readUInt16BE(4 + i * 8)
    const encoding = cmap.readUInt16BE(6 + i * 8)
    const offset = cmap.readUInt32BE(8 + i * 8)
    const format = cmap.readUInt16BE(offset)
    if (format === 4 && (platform === 3 || platform === 0) && !seen4) {
      seen4 = true
      const segX2 = cmap.readUInt16BE(offset + 6)
      const endCodes = offset + 14
      const startCodes = endCodes + segX2 + 2
      const deltas = startCodes + segX2
      const rangeOffsets = deltas + segX2
      for (let s = 0; s < segX2 / 2; s++) {
        const end = cmap.readUInt16BE(endCodes + s * 2)
        const start = cmap.readUInt16BE(startCodes + s * 2)
        const delta = cmap.readInt16BE(deltas + s * 2)
        const rangeOffset = cmap.readUInt16BE(rangeOffsets + s * 2)
        for (let c = start; c <= end && c !== 0xffff; c++) {
          let glyph
          if (rangeOffset === 0) {
            glyph = (c + delta) & 0xffff
          } else {
            const addr = rangeOffsets + s * 2 + rangeOffset + (c - start) * 2
            glyph = cmap.readUInt16BE(addr)
            if (glyph !== 0) glyph = (glyph + delta) & 0xffff
          }
          if (glyph !== 0) glyphs.add(c)
        }
      }
    }
  }
  // Format 12 covers astral code planes; it has no Persian content, so a
  // font that has only format 12 still answers for the BMP through format 4.
  return glyphs
}

