package imapconn

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// XOAuth2 is the SASL mechanism used by Outlook (and Gmail) for OAuth2 tokens.
// See https://learn.microsoft.com/exchange/client-developer/legacy-protocols/how-to-authenticate-an-imap-pop-smtp-application-by-using-oauth
const XOAuth2 = "XOAUTH2"

type xoauth2Client struct {
	username, token string
	failed          bool
}

// NewXOAuth2Client returns a SASL client for the XOAUTH2 mechanism.
func NewXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

func (c *xoauth2Client) Start() (string, []byte, error) {
	return XOAuth2, []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"), nil
}

// Next handles the server's error challenge (a base64 JSON status). The
// protocol requires an empty response, after which the server fails the
// command with a NO and a readable message.
func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	if c.failed {
		return nil, errors.New("XOAUTH2: unexpected server challenge")
	}
	c.failed = true
	return []byte{}, nil
}
