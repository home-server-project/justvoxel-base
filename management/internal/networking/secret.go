package networking

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const redactedSecret = "[REDACTED]"

type secretState struct {
	value []byte
}

// Secret keeps a new Wi-Fi credential in mutable bytes for the shortest
// practical lifetime. NetworkManager still requires a transient Go string at
// the D-Bus boundary, so this does not claim cryptographic erasure of every
// runtime copy.
type Secret struct {
	state *secretState
}

func NewSecret(value string) Secret {
	if value == "" {
		return Secret{}
	}
	return Secret{state: &secretState{value: []byte(value)}}
}

func (s Secret) bytes() []byte {
	if s.state == nil {
		return nil
	}
	return s.state.value
}

func (s Secret) Empty() bool { return len(s.bytes()) == 0 }

func (s Secret) Len() int { return len(s.bytes()) }

func (s Secret) Value() string { return string(s.bytes()) }

func (s Secret) IsHex() bool {
	value := s.bytes()
	if len(value) == 0 {
		return false
	}
	for _, b := range value {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return false
		}
	}
	return true
}

func (s *Secret) Clear() {
	if s == nil || s.state == nil {
		return
	}
	for i := range s.state.value {
		s.state.value[i] = 0
	}
	s.state.value = nil
	s.state = nil
}

func (s Secret) String() string { return redactedSecret }

func (s Secret) GoString() string { return redactedSecret }

func (s Secret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, redactedSecret)
}

func redactSecretError(err error, secret Secret) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if !secret.Empty() {
		message = strings.ReplaceAll(message, secret.Value(), redactedSecret)
	}
	return errors.New(message)
}
