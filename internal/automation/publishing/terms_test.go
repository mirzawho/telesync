package publishing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// A WordPress term id is one the destination site already has. Verifying a
// configuration asks the site which terms it has; it never creates one, so a
// workflow cannot grow a taxonomy the operator did not make.

// fakeSites is one registered WordPress destination.
type fakeSites struct {
	site channelstore.WordPressSite
	have bool
}

func (f fakeSites) GetWordPressSite(context.Context, int64) (channelstore.WordPressSite, bool, error) {
	return f.site, f.have, nil
}

// fakeSecrets opens a stored credential.
type fakeSecrets struct{}

func (fakeSecrets) Seal(string) (secrets.Box, error) { return secrets.Box{}, errors.New("not used") }
func (fakeSecrets) Open(secrets.Box) (string, error) { return "app-password", nil }

// fakeSiteClient is the site's own REST API.
type fakeSiteClient struct {
	terms        map[string][]wordpress.TermRef
	createdTags  int
	createdPosts int
}

func (c *fakeSiteClient) ListTerms(_ context.Context, _, _, _, taxonomy, _ string, page, perPage int) (wordpress.TermPage, error) {
	terms := c.terms[taxonomy]
	return wordpress.TermPage{
		Terms: terms, Page: page, PerPage: perPage,
		Total: len(terms), TotalPages: 1,
	}, nil
}

func (c *fakeSiteClient) CreatePost(context.Context, string, string, string, wordpress.PostInput) (wordpress.PostResult, error) {
	c.createdPosts++
	return wordpress.PostResult{}, errors.New("not used")
}

func (c *fakeSiteClient) UploadMedia(context.Context, string, string, string, string, string, []byte) (wordpress.MediaRef, error) {
	return wordpress.MediaRef{}, errors.New("not used")
}

func (c *fakeSiteClient) FindTag(context.Context, string, string, string, string) (wordpress.TagRef, bool, error) {
	return wordpress.TagRef{}, false, nil
}

func (c *fakeSiteClient) CreateTag(context.Context, string, string, string, string) (wordpress.TagRef, error) {
	c.createdTags++
	return wordpress.TagRef{}, errors.New("not used")
}

func newTermService(have bool) (*publishing.Service, *fakeSiteClient) {
	client := &fakeSiteClient{terms: map[string][]wordpress.TermRef{
		publishing.TaxonomyCategories: {{ID: 7, Name: "News"}, {ID: 8, Name: "Tech"}},
		publishing.TaxonomyTags:       {{ID: 11, Name: "release"}},
	}}
	return &publishing.Service{
		Sites:      fakeSites{site: channelstore.WordPressSite{ID: 3, URL: "https://site.example.com", Username: "editor"}, have: have},
		Secrets:    fakeSecrets{},
		SiteClient: client,
	}, client
}

func TestVerifyTermsAcceptsOnlyTermsTheSiteReports(t *testing.T) {
	svc, client := newTermService(true)
	ctx := context.Background()

	if err := svc.VerifyTerms(ctx, 3, publishing.TaxonomyCategories, []int{7, 8}); err != nil {
		t.Fatalf("VerifyTerms: %v", err)
	}
	if err := svc.VerifyTerms(ctx, 3, publishing.TaxonomyTags, []int{11}); err != nil {
		t.Fatalf("VerifyTerms: %v", err)
	}
	// A term id the site does not report is refused, and nothing is created in
	// its place.
	err := svc.VerifyTerms(ctx, 3, publishing.TaxonomyCategories, []int{7, 404})
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want the unknown term refused", err)
	}
	if err := svc.VerifyTerms(ctx, 3, publishing.TaxonomyTags, []int{11, 12}); !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("error = %v, want the unknown tag refused", err)
	}
	if client.createdTags != 0 || client.createdPosts != 0 {
		t.Fatalf("created tags = %d, posts = %d — verification must never create anything",
			client.createdTags, client.createdPosts)
	}
}

func TestVerifyTermsRefusesAValueThatIsNotATermID(t *testing.T) {
	_, client := newTermService(true)
	if _, _, err := publishing.TermIDsOf(map[string]any{"tag_ids": []any{"news"}}); err == nil {
		t.Fatal("a term id that is not a number must be refused")
	}
	if _, _, err := publishing.TermIDsOf(map[string]any{"category_ids": []any{float64(7.5)}}); err == nil {
		t.Fatal("a part-numbered term id must be refused rather than rounded")
	}
	categories, tags, err := publishing.TermIDsOf(map[string]any{
		"category_ids": []any{float64(7)}, "tag_ids": []int{11},
	})
	if err != nil || len(categories) != 1 || len(tags) != 1 {
		t.Fatalf("categories = %v tags = %v err = %v", categories, tags, err)
	}
	if client.createdTags != 0 {
		t.Fatal("no term may be created")
	}
}

func TestVerifyTermsReportsAMissingSite(t *testing.T) {
	svc, _ := newTermService(false)
	err := svc.VerifyTerms(context.Background(), 3, publishing.TaxonomyCategories, []int{7})
	if code := publishing.CodeOf(err); code != publishing.CodePublishDestMissing {
		t.Fatalf("code = %q, want %q (err %v)", code, publishing.CodePublishDestMissing, err)
	}
}
