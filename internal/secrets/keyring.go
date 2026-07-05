package secrets

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"
)

const Service = "spruce"

var ErrNotFound = keyring.ErrNotFound

func tokenAccount(serverURL, username string) string {
	return strings.TrimRight(serverURL, "/") + "|" + username
}

func GetToken(serverURL, username string) (string, error) {
	account := tokenAccount(serverURL, username)
	token, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		if token, fallbackErr := getTokenWithSecretTool(account); fallbackErr == nil {
			return token, nil
		}
	}
	return token, err
}

func getTokenWithSecretTool(account string) (string, error) {
	out, err := exec.Command("secret-tool", "lookup", "service", Service, "username", account).Output()
	if err != nil {
		return "", err
	}
	token := strings.TrimRight(string(out), "\r\n")
	if token == "" {
		return "", ErrNotFound
	}
	return token, nil
}

func SetToken(serverURL, username, token string) error {
	return keyring.Set(Service, tokenAccount(serverURL, username), token)
}

func DeleteToken(serverURL, username string) error {
	err := keyring.Delete(Service, tokenAccount(serverURL, username))
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
