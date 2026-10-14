package controller

// NOT EXECUTED — LOCAL TESTING PROHIBITED.
import "testing"

func TestLegacyPushCannotBypassConfiguredUserToken(t *testing.T) {
	channelToken := "channel-credential"
	cases := []struct {
		name, supplied, user string
		channel              *string
		allowed              bool
	}{
		{"unconfigured compatibility", "", "", nil, true},
		{"missing user token", "", "user-credential", nil, false},
		{"wrong user token", "wrong", "user-credential", nil, false},
		{"matching user token", "user-credential", "user-credential", nil, true},
		{"matching channel token", "channel-credential", "user-credential", &channelToken, true},
		{"wrong both tokens", "wrong", "user-credential", &channelToken, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if actual := authMessage(test.supplied, test.user, test.channel); actual != test.allowed {
				t.Fatalf("allowed=%v want=%v", actual, test.allowed)
			}
		})
	}
}
