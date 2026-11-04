package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// Changing one's own password: POST /api/v1/auth/password takes the caller's
// current password and a new one, and answers with a fresh token. The subject
// is always the bearer's account — a request cannot name anyone else. The
// policy is the one the user routes enforce (channelstore.MinPasswordLen).

const changePasswordPath = "/api/v1/auth/password"

func TestChangePasswordNeedsAValidToken(t *testing.T) {
	env := newAccountEnv(t, registry{})
	resp := env.send(t, "", http.MethodPost, changePasswordPath,
		`{"current_password":"password-plain","new_password":"a new password"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", resp.StatusCode)
	}
}

func TestChangePasswordRejectsAMissingField(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "plain")
	for _, body := range []string{
		`{"current_password":"","new_password":"a new password"}`,
		`{"current_password":"password-plain","new_password":""}`,
		`{}`,
	} {
		resp := env.send(t, who.token, http.MethodPost, changePasswordPath, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s = %d: %s", body, resp.StatusCode, bodyOf(resp))
		}
	}
}

func TestChangePasswordRefusesAWrongCurrentPassword(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "plain")

	resp := env.send(t, who.token, http.MethodPost, changePasswordPath,
		`{"current_password":"not-the-password","new_password":"a brand new one"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong current = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	// The stored password is untouched: the real one still logs in, and the
	// requested new one does not.
	if !loginWorks(t, env, "plain", "password-plain") {
		t.Fatal("the old password stopped working")
	}
	if loginWorks(t, env, "plain", "a brand new one") {
		t.Fatal("the new password was accepted on a refused change")
	}
}

func TestChangePasswordRefusesAWeakNewPassword(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "plain")

	short := strings.Repeat("x", channelstore.MinPasswordLen-1)
	resp := env.send(t, who.token, http.MethodPost, changePasswordPath,
		fmt.Sprintf(`{"current_password":"password-plain","new_password":%q}`, short))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	// A refused change leaves the account alone.
	if !loginWorks(t, env, "plain", "password-plain") {
		t.Fatal("a refused change broke the account's password")
	}
}

func TestChangePasswordReplacesTheHashAndAnswersANewToken(t *testing.T) {
	env := newAccountEnv(t, registry{})
	who := env.account(t, "plain")

	resp := env.send(t, who.token, http.MethodPost, changePasswordPath,
		`{"current_password":"password-plain","new_password":"a new password"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	raw := bodyOf(resp)
	for _, leaked := range []string{"password-plain", "a new password", "password_hash"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("the answer echoed %q: %s", leaked, raw)
		}
	}
	var grant loginResponse
	if err := json.Unmarshal([]byte(raw), &grant); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if grant.AccessToken == "" || grant.TokenType != "Bearer" {
		t.Fatalf("token grant = %+v", grant)
	}

	// The new password logs in; the old one does not.
	if !loginWorks(t, env, "plain", "a new password") {
		t.Fatal("the new password does not log in")
	}
	if loginWorks(t, env, "plain", "password-plain") {
		t.Fatal("the old password still logs in")
	}

	// The issued token authenticates a protected route.
	if again := env.send(t, grant.AccessToken, http.MethodGet, mePath, ""); again.StatusCode != http.StatusOK {
		t.Fatalf("the returned token = %d, want 200 on /auth/me", again.StatusCode)
	}
}

// TestChangePasswordCannotTargetAnotherUser proves the body cannot name a
// subject: a user_id field is ignored and only the caller's own password can
// change.
func TestChangePasswordCannotTargetAnotherUser(t *testing.T) {
	env := newAccountEnv(t, registry{})
	caller := env.account(t, "caller")
	victim := env.account(t, "victim", channelstore.PermissionUsersManage)

	resp := env.send(t, caller.token, http.MethodPost, changePasswordPath,
		fmt.Sprintf(`{"user_id":%d,"current_password":"password-caller","new_password":"a new password"}`,
			victim.user.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	// The victim's password is untouched; the caller's is the one that moved.
	if !loginWorks(t, env, "victim", "password-victim") {
		t.Fatal("another account's password was changed through the body's user_id")
	}
	if loginWorks(t, env, "caller", "password-caller") {
		t.Fatal("the caller's password was not the one changed")
	}
}

// loginWorks posts to the login route and answers whether the credentials were
// accepted.
func loginWorks(t *testing.T, env *accountEnv, username, password string) bool {
	t.Helper()
	resp := env.send(t, "", http.MethodPost, loginPath,
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
	return resp.StatusCode == http.StatusOK
}
