package publishing

import (
	"context"
	"fmt"

	"github.com/mirza/telesync/internal/automation"
)

// WordPress taxonomies a destination may name terms from. They are the REST
// resources WordPress itself uses.
const (
	TaxonomyCategories = "categories"
	TaxonomyTags       = "tags"
)

// How much of a site's taxonomy is read to confirm a configured term id. The
// bound is what keeps a configuration change from walking an unbounded list: a
// term that cannot be confirmed inside it is refused rather than assumed.
const (
	termsPerPage = 100
	maxTermPages = 20
)

// TermIDsOf reads the WordPress term ids a destination's options name.
//
// The values arrive as JSON and are numbers this process did not choose. A
// value that is not a whole number is refused rather than rounded into a term
// id that happens to exist: a term id is never invented.
func TermIDsOf(options map[string]any) (categories, tags []int, err error) {
	categories, err = optionInts(options, OptionCategoryIDs)
	if err != nil {
		return nil, nil, err
	}
	tags, err = optionInts(options, OptionTagIDs)
	if err != nil {
		return nil, nil, err
	}
	return categories, tags, nil
}

// VerifyTerms reports whether every term id a destination names belongs to the
// site it publishes to.
//
// It asks the site itself, and it only ever confirms terms that already exist:
// nothing here creates one. A term the site does not report is refused, so a
// workflow cannot publish into a taxonomy selection that has been deleted or
// that was never the site's to begin with.
//
// taxonomy is "categories" or "tags".
func (s *Service) VerifyTerms(ctx context.Context, siteID int64, taxonomy string, termIDs []int) error {
	if len(termIDs) == 0 {
		return nil
	}
	if taxonomy != TaxonomyCategories && taxonomy != TaxonomyTags {
		return fmt.Errorf("%w: %q is not a WordPress taxonomy", automation.ErrInvalid, taxonomy)
	}
	site, password, err := s.siteCredential(ctx, siteID)
	if err != nil {
		return err
	}
	if s.SiteClient == nil {
		return fmt.Errorf("%w: this deployment cannot read a site's terms", automation.ErrInvalid)
	}

	wanted := map[int]bool{}
	for _, id := range termIDs {
		if id <= 0 {
			return fmt.Errorf("%w: %d is not a WordPress term id", automation.ErrInvalid, id)
		}
		wanted[id] = true
	}

	found := map[int]bool{}
	for page := 1; page <= maxTermPages && len(found) < len(wanted); page++ {
		terms, err := s.SiteClient.ListTerms(ctx, site.URL, site.Username, password, taxonomy, "", page, termsPerPage)
		if err != nil {
			return fmt.Errorf("%w: the site's %s could not be read: %v", automation.ErrInvalid, taxonomy, err)
		}
		for _, term := range terms.Terms {
			if wanted[term.ID] {
				found[term.ID] = true
			}
		}
		if len(terms.Terms) == 0 || page >= terms.TotalPages {
			break
		}
	}
	for _, id := range termIDs {
		if !found[id] {
			return fmt.Errorf("%w: term %d is not one of this site's %s", automation.ErrInvalid, id, taxonomy)
		}
	}
	return nil
}
