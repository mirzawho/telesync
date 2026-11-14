package publishing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/wordpress"
)

// WordPressResult is what a registered WordPress site confirmed about the
// created post.
type WordPressResult struct {
	PostID     int
	PostURL    string
	PostStatus string
	Title      string
	// FeaturedMedia is the uploaded attachment the post features, or zero.
	FeaturedMedia int
	// Replayed is true when this answer is an earlier confirmed result being
	// returned again, and no post was created now.
	Replayed bool
	// Payload is the response's "data" object as JSON, exactly what an
	// idempotent replay answers with.
	Payload json.RawMessage
}

// PublishWordPress creates one post on one registered WordPress site.
//
// The site is named by its local record and never by a URL from a caller: the
// credential is opened here, used for this request and never returned. Term ids
// are used as they are and never invented.
func (s *Service) PublishWordPress(ctx context.Context, a Actor, siteID int64, req WordPressPost) (WordPressResult, error) {
	if err := s.requirePublishPermission(a); err != nil {
		return WordPressResult{}, err
	}
	if err := ValidateWordPress(req); err != nil {
		return WordPressResult{}, &Error{http.StatusBadRequest, CodeInvalidInput, err.Error()}
	}

	site, password, err := s.credentialFor(ctx, a, siteID)
	if err != nil {
		return WordPressResult{}, err
	}
	source, err := s.resolvePublishSource(ctx, a, sourceRequest(req.Source))
	if err != nil {
		return WordPressResult{}, err
	}

	// A WordPress post is recorded against the message it was composed from:
	// the site's answer is the same post whichever wording was submitted.
	destinationID := fmt.Sprintf("wp:%d", siteID)
	operation, err := s.beginPublish(ctx, a, req.IdempotencyKey, "", destinationID,
		sourceRequestHash(req.Source.LocalChannelID, req.Source.MessageID))
	if err != nil {
		return WordPressResult{}, err
	}
	if operation != nil && operation.Status == channelstore.PublishSucceeded {
		return WordPressResult{Replayed: true, Payload: storedPayload(operation.Result)}, nil
	}

	result, err := s.runWordPressPublish(ctx, source, site, password, req)
	if err != nil {
		s.finishPublish(ctx, operation, channelstore.PublishFailed, "", CodeOf(err))
		return WordPressResult{}, err
	}
	s.finishPublish(ctx, operation, channelstore.PublishSucceeded, string(result.Payload), "")
	return *result, nil
}

// sourceRequest builds the source reference a post is resolved from.
func sourceRequest(source RequestSource) Request {
	var req Request
	req.Source = source
	req.Media.Mode = MediaPreserveSource
	return req
}

// runWordPressPublish turns a prepared post into a WordPress post, resolving
// its tags, uploading its attachment and reporting what the site confirmed.
func (s *Service) runWordPressPublish(ctx context.Context, source sourceMessage,
	site channelstore.WordPressSite, password string, req WordPressPost) (*WordPressResult, error) {

	client := s.SiteClient
	if client == nil {
		return nil, &Error{http.StatusServiceUnavailable, CodePublishDestMissing,
			"WordPress publishing is not configured."}
	}

	// Term ids the caller named are used as they are. Names are only resolved
	// when no ids were sent: a request never carries both, and a name is never
	// mistaken for an id.
	tagIDs := append([]int(nil), req.Post.TagIDs...)
	if len(tagIDs) == 0 {
		tagIDs = make([]int, 0, len(req.Post.Tags))
	}
	for _, name := range req.Post.Tags {
		if len(req.Post.TagIDs) > 0 {
			break
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if existing, found, err := client.FindTag(ctx, site.URL, site.Username, password, name); err != nil {
			return nil, &Error{http.StatusInternalServerError, CodePublishFailed,
				fmt.Sprintf("The WordPress tag %q could not be looked up.", name)}
		} else if found {
			tagIDs = append(tagIDs, existing.ID)
			continue
		}
		created, err := client.CreateTag(ctx, site.URL, site.Username, password, name)
		if err != nil {
			// A tag that cannot be created is not quietly dropped: the caller is
			// told, and no post is made pretending it was assigned.
			return nil, &Error{http.StatusForbidden, CodePublishFailed,
				fmt.Sprintf("The WordPress tag %q could not be created; the post was not published.", name)}
		}
		tagIDs = append(tagIDs, created.ID)
	}

	featured := 0
	if req.Media.Mode == MediaPreserveSource && source.message.Media != nil && mediaHasBytes(source.message.Media.Type) {
		if err := namedAttachment(req.Media.AttachmentID, source.message.Media); err != nil {
			return nil, err
		}
		// The attachment is read through the connection that recorded the source
		// message, exactly as the Telegram route reads it. A WordPress site is
		// not a Telegram client and holds no Telegram session at all.
		stream, err := source.worker.Media(ctx, source.row.ChannelID, source.message.ID,
			largestPhotoSize, rpc.MediaSlice{})
		if err != nil {
			return nil, s.sourceMediaFailure(source, err)
		}
		data, err := stream.ReadAll()
		if err != nil {
			if errors.Is(err, rpc.ErrMediaTooLarge) {
				s.logSourceMedia(source, err)
				return nil, &Error{http.StatusRequestEntityTooLarge, CodePublishMediaTooLarge,
					"The attachment is too large to send."}
			}
			return nil, s.sourceMediaFailure(source, err)
		}
		uploaded, err := client.UploadMedia(ctx, site.URL, site.Username, password,
			sendFileName(source.message.Media), stream.MimeType, data)
		if err != nil {
			// An upload that failed is not reported as published media, and it is
			// not reported as an unreadable source either: the bytes arrived and
			// the site refused them.
			return nil, &Error{http.StatusBadGateway, CodePublishMediaUpload,
				"The attachment could not be uploaded to the WordPress site; the post was not published."}
		}
		if uploaded.ID > 0 {
			featured = uploaded.ID
		}
	}

	post, err := client.CreatePost(ctx, site.URL, site.Username, password, wordpress.PostInput{
		Title:         req.Post.Title,
		Slug:          strings.TrimSpace(req.Post.Slug),
		Content:       req.Post.Content,
		Status:        req.Post.Status,
		CategoryIDs:   req.Post.CategoryIDs,
		TagIDs:        tagIDs,
		FeaturedMedia: featured,
		CommentStatus: req.Options.CommentStatus,
	})
	if err != nil {
		return nil, mapWordPressPublishError(err)
	}
	body := map[string]any{
		"post": map[string]any{
			"id":         post.ID,
			"title":      post.Title,
			"link":       post.Link,
			"status":     post.Status,
			"categories": post.CategoryIDs,
			"tags":       post.TagIDs,
		},
		"site": map[string]any{"id": site.ID, "url": site.URL},
	}
	if featured > 0 {
		body["media"] = map[string]any{"attachment_id": featured}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{http.StatusInternalServerError, CodeStoreError,
			"The result could not be encoded."}
	}
	return &WordPressResult{
		PostID:        post.ID,
		PostURL:       post.Link,
		PostStatus:    post.Status,
		Title:         post.Title,
		FeaturedMedia: featured,
		Payload:       payload,
	}, nil
}

// mapWordPressPublishError turns a client failure into the answer a caller is
// owed. A post the site did not answer for is never reported as created.
func mapWordPressPublishError(err error) *Error {
	switch {
	case errors.Is(err, wordpress.ErrUnauthorized):
		return &Error{http.StatusUnauthorized, CodePublishWordPressAuth,
			"The WordPress credentials were refused."}
	case errors.Is(err, wordpress.ErrForbidden):
		return &Error{http.StatusForbidden, CodePublishWordPressForbidden,
			"The WordPress user may not publish this content."}
	case errors.Is(err, wordpress.ErrTimeout):
		return &Error{http.StatusGatewayTimeout, CodePublishWordPressTimeout,
			"The WordPress site did not answer in time. The outcome is unknown and is not retried."}
	case errors.Is(err, wordpress.ErrUnreachable), errors.Is(err, wordpress.ErrBlockedAddress):
		return &Error{http.StatusBadGateway, CodePublishWordPressUnreachable,
			"The WordPress site could not be reached."}
	case errors.Is(err, wordpress.ErrIncompatible):
		return &Error{http.StatusBadGateway, CodePublishWordPressIncompatible,
			"The WordPress site did not answer as a REST API."}
	}
	return &Error{http.StatusBadGateway, CodePublishFailed,
		"The WordPress site refused the post."}
}
