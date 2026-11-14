package apidocs_test

import (
	"net/http"
	"testing"

	"github.com/mirza/telesync/internal/apidocs"
)

func TestPersonalAccountRoutesAreInTheGeneratedContract(t *testing.T) {
	want := []struct {
		method  string
		pattern string
	}{
		{http.MethodGet, "/api/v1/me/telegram-accounts"},
		{http.MethodGet, "/api/v1/me/telegram-accounts/{account_id}"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/start"},
		{http.MethodGet, "/api/v1/me/telegram-accounts/login/{attempt_id}"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/{attempt_id}/code"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/{attempt_id}/password"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/{attempt_id}/cancel"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/{account_id}/disconnect"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/{account_id}/logout"},
		{http.MethodDelete, "/api/v1/me/telegram-accounts/{account_id}"},
		{http.MethodGet, "/api/v1/telegram/accounts/{account_id}/channels"},
		{http.MethodPost, "/api/v1/telegram/accounts/{account_id}/messages/copy"},
		{http.MethodPost, "/api/v1/telegram/accounts/{account_id}/channels/{channel_id}/messages/send"},
	}

	for _, route := range want {
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			if !routeIsListed(route.method, route.pattern) {
				t.Fatalf("%s %s is served but absent from the API contract route table",
					route.method, route.pattern)
			}
		})
	}
}

func routeIsListed(method, pattern string) bool {
	for _, route := range apidocs.Routes {
		if route.Pattern != pattern {
			continue
		}
		for _, got := range route.Methods {
			if got == method {
				return true
			}
		}
	}
	return false
}
