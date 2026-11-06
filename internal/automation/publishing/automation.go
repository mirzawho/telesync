package publishing

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/automation"
)

// Publish sends one prepared message to one destination on behalf of a
// background run. It is automation.PublishingService.
//
// The request carries the account the run publishes under, and that account's
// permissions are read again here: a queued run that outlives its author's
// access publishes nothing. What the destination is asked to do is built from
// the same request the interactive route builds, so a background post and a
// manual one cannot diverge.
//
// The result always describes the outcome — including the code and message of
// a failure — and err carries the same failure for a caller that wants to stop.
func (s *Service) Publish(ctx context.Context, request automation.PublishRequest) (automation.PublishResult, error) {
	actor, err := s.actorFor(ctx, request.UserID)
	if err != nil {
		return failureResult(err), err
	}
	switch request.Destination.Type {
	case automation.DestinationTelegram:
		req := telegramRequestOf(request)
		res, err := s.PublishTelegram(ctx, actor, request.Destination.ClientKey, req)
		if err != nil {
			return failureResult(err), err
		}
		out := automation.PublishResult{Outcome: automation.PublishSucceeded}
		if res.Message != nil {
			out.RemoteMessageID = int64(res.Message.ID)
		}
		return out, nil
	case automation.DestinationWordPress:
		post, err := wordPressPostOf(request)
		if err != nil {
			return failureResult(err), err
		}
		res, err := s.PublishWordPress(ctx, actor, request.Destination.SiteID, post)
		if err != nil {
			return failureResult(err), err
		}
		return automation.PublishResult{
			Outcome:      automation.PublishSucceeded,
			RemotePostID: int64(res.PostID),
			RemoteURL:    res.PostURL,
		}, nil
	default:
		err := &Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: fmt.Sprintf("%q is not a destination this publisher knows.", request.Destination.Type)}
		return failureResult(err), err
	}
}

// failureResult is the result a failed attempt leaves behind. An outcome this
// process never heard back from is reported as unknown and is never retried as
// if it had failed.
func failureResult(err error) automation.PublishResult {
	out := automation.PublishResult{
		Outcome:      automation.PublishFailed,
		ErrorCode:    CodeOf(err),
		ErrorMessage: messageOf(err),
	}
	switch out.ErrorCode {
	case CodePublishUnknown, CodePublishWordPressTimeout:
		out.Outcome = automation.PublishUnknown
	}
	return out
}

func messageOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// telegramRequestOf turns the shared request into the one the Telegram
// publishing route would have built from a client's body.
func telegramRequestOf(in automation.PublishRequest) Request {
	var req Request
	req.Source.LocalChannelID = in.Source.ManagedChannelID
	req.Source.MessageID = int(in.Source.MessageID)
	req.Destination.ChannelID = strconv.FormatInt(in.Destination.ChannelID, 10)
	req.Content.Text = in.Text
	req.Content.Entities = in.Entities
	// The destination's own thumbnail policy decides what travels with the
	// post; a request that names a mode explicitly overrides it.
	req.Media.Mode = mediaModeOf(in.Destination.Options)
	if in.Options.MediaMode != "" {
		req.Media.Mode = in.Options.MediaMode
	}
	req.Options.Silent = in.Options.Silent
	req.Options.DisableWebPagePreview = in.Options.DisableWebPagePreview
	req.IdempotencyKey = in.IdempotencyKey
	return req
}

// Option keys a WordPress destination carries. They are the destination's own publishing options as
// the workflow stores them, and this is the only place that interprets them.
//
// A workflow publishes only term ids the destination site already has. No
// option can name a term by name, because a workflow must never create a
// WordPress term the operator did not make.
const (
	OptionTitle         = "title"
	OptionSlug          = "slug"
	OptionPostStatus    = "post_status"
	OptionCommentStatus = "comment_status"
	OptionCategoryIDs   = "category_ids"
	OptionTagIDs        = "tag_ids"
	// OptionThumbnail is the destination's thumbnail policy: "source" publishes
	// the source attachment along (as the featured image on WordPress, as the
	// attachment on Telegram), "omit" publishes text alone.
	OptionThumbnail = "thumbnail"
	// OptionMedia is the same policy in the manual contract's spelling.
	OptionMedia = "media"
)

// wordPressPostOf turns the shared request into a prepared WordPress post.
func wordPressPostOf(in automation.PublishRequest) (WordPressPost, error) {
	var req WordPressPost
	req.Source.LocalChannelID = in.Source.ManagedChannelID
	req.Source.MessageID = int(in.Source.MessageID)
	req.Post.Content = in.Text
	req.Post.Status = optionString(in.Destination.Options, OptionPostStatus, "publish")
	req.Options.CommentStatus = optionString(in.Destination.Options, OptionCommentStatus, "")
	req.Post.Slug = optionString(in.Destination.Options, OptionSlug, "")
	req.Post.Title = optionString(in.Destination.Options, OptionTitle, "")
	if req.Post.Title == "" {
		// A WordPress post needs a title and a workflow does not have to name
		// one: the first line of what is being published is the honest default.
		req.Post.Title = firstLine(in.Text)
	}
	if strings.TrimSpace(req.Post.Title) == "" {
		return WordPressPost{}, &Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: "This WordPress destination has no title and the content has none to take."}
	}

	categories, err := optionInts(in.Destination.Options, OptionCategoryIDs)
	if err != nil {
		return WordPressPost{}, err
	}
	tags, err := optionInts(in.Destination.Options, OptionTagIDs)
	if err != nil {
		return WordPressPost{}, err
	}
	req.Post.CategoryIDs = categories
	req.Post.TagIDs = tags

	req.Media.Mode = MediaPreserveSource
	switch mode := mediaModeOf(in.Destination.Options); mode {
	case MediaPreserveSource, MediaOmit:
		req.Media.Mode = mode
	default:
		return WordPressPost{}, &Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: fmt.Sprintf("%q is not a media mode this publisher knows.", mode)}
	}
	if in.Options.MediaMode == MediaOmit {
		req.Media.Mode = MediaOmit
	}
	req.IdempotencyKey = in.IdempotencyKey
	return req, nil
}

// mediaModeOf reads the destination's thumbnail policy.
func mediaModeOf(options map[string]any) string {
	for _, key := range []string{OptionThumbnail, OptionMedia} {
		raw := strings.TrimSpace(optionString(options, key, ""))
		switch raw {
		case "":
			continue
		case "source", MediaPreserveSource:
			return MediaPreserveSource
		case "omit", "none":
			return MediaOmit
		default:
			return raw
		}
	}
	return MediaPreserveSource
}

func optionString(options map[string]any, key, fallback string) string {
	if options == nil {
		return fallback
	}
	switch v := options[key].(type) {
	case nil:
		return fallback
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fallback
	}
}

// optionInts reads a list of term ids out of a destination's options.
//
// The values arrive as JSON, so they are numbers this process did not choose.
// A value that is not a whole number is refused rather than rounded into a term
// id that exists by accident.
func optionInts(options map[string]any, key string) ([]int, error) {
	if options == nil {
		return nil, nil
	}
	raw, ok := options[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch list := raw.(type) {
	case []int:
		return append([]int(nil), list...), nil
	case []any:
		out := make([]int, 0, len(list))
		for _, item := range list {
			id, err := optionInt(item)
			if err != nil {
				return nil, &Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
					Message: fmt.Sprintf("%s holds %v, which is not a term id.", key, item)}
			}
			out = append(out, id)
		}
		return out, nil
	default:
		return nil, &Error{Status: http.StatusBadRequest, Code: CodeInvalidInput,
			Message: fmt.Sprintf("%s must be a list of term ids.", key)}
	}
}

func optionInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%v is not a whole number", n)
		}
		return int(n), nil
	case string:
		id, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, err
		}
		return id, nil
	default:
		return 0, fmt.Errorf("%v is not a number", v)
	}
}

// firstLine is the title a post without one is given: the first line of the
// content, cut on a rune boundary.
func firstLine(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return validUTF8Cut(line, 200)
}
