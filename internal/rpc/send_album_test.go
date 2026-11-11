package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirza/telesync/internal/rpc"
)

type albumBackend struct {
	*fakeBackend
	request rpc.SendAlbumRequest
}

func (b *albumBackend) SendAlbum(_ context.Context, req rpc.SendAlbumRequest) (rpc.SendAlbumResult, error) {
	b.request = req
	return rpc.SendAlbumResult{ChannelID: "777", GroupedID: "555", Messages: []rpc.Message{{ID: 21}, {ID: 22}}}, nil
}

func TestSendAlbumRPCMapsOrderedMultipartFilesToItems(t *testing.T) {
	backend := &albumBackend{fakeBackend: &fakeBackend{}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	requestPart, err := writer.CreateFormField("request")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(rpc.SendAlbumRequest{ChannelID: 777, Items: []rpc.SendAlbumItem{
		{Media: &rpc.SendMedia{Kind: "photo", Name: "first.jpg", MimeType: "image/jpeg"}, Text: "caption 1", RandomID: 1},
		{Media: &rpc.SendMedia{Kind: "video", Name: "second.mp4", MimeType: "video/mp4", Width: 640, Height: 360}, Text: "caption 2", RandomID: 2},
	}})
	_, _ = requestPart.Write(payload)
	for _, file := range [][]byte{[]byte("first bytes"), []byte("second bytes")} {
		part, err := writer.CreateFormFile("media", "attachment")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(file)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/send_album", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	rpc.NewHandler(backend).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(backend.request.Items) != 2 || string(backend.request.Items[0].Media.Data) != "first bytes" ||
		string(backend.request.Items[1].Media.Data) != "second bytes" || backend.request.Items[0].Text != "caption 1" ||
		backend.request.Items[1].Media.Width != 640 || backend.request.Items[1].RandomID != 2 {
		t.Fatalf("decoded album = %+v", backend.request)
	}
	var answer struct {
		Messages  []rpc.Message `json:"messages"`
		GroupedID string        `json:"grouped_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Messages) != 2 || answer.GroupedID != "555" {
		t.Fatalf("answer = %+v", answer)
	}
}
