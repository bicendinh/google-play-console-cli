package fault

import (
	"google.golang.org/api/googleapi"
	"strings"
	"testing"
)

func TestGoogleMessagesAreSanitized(t *testing.T) {
	message := "Version code 123 already been used.\nAuthorization: Bearer secret-access-token\x1b[31m access_token=ya29.fake-token client_secret=secret-client -----BEGIN PRIVATE KEY-----\nsecret-key\n-----END PRIVATE KEY-----"
	for _, status := range []int{400, 404, 409, 412} {
		e := From(&googleapi.Error{Code: status, Message: message})
		for _, secret := range []string{"secret-access-token", "ya29.fake-token", "secret-client", "secret-key", "\x1b", "\n"} {
			if strings.Contains(e.Message, secret) {
				t.Fatalf("leaked %q: %s", secret, e.Message)
			}
		}
		if !strings.Contains(e.Message, "Version code 123") {
			t.Fatal("lost actionable validation")
		}
	}
}
