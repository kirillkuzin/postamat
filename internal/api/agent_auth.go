package api

import (
	"context"
	"errors"
	"strings"

	"github.com/kirillkuzin/postamat/internal/auth"
)

var (
	ErrAgentAuthenticatorRequired = errors.New("agent authenticator is required")
	ErrAgentIDRequired            = errors.New("agent id is required")
	ErrAgentTokenRequired         = errors.New("agent token is required")
	ErrAgentTokenInvalid          = errors.New("agent token is invalid")
)

type AgentAuthenticator interface {
	VerifyAgentToken(ctx context.Context, agentID string, token string) error
}

type StaticAgentTokenAuthenticator struct {
	pepper      string
	tokenHashes map[string]string
}

func NewStaticAgentTokenAuthenticator(tokens map[string]string, pepper string) (*StaticAgentTokenAuthenticator, error) {
	if pepper == "" {
		return nil, auth.ErrPepperRequired
	}
	hashes := make(map[string]string, len(tokens))
	for agentID, token := range tokens {
		if strings.TrimSpace(agentID) == "" {
			return nil, ErrAgentIDRequired
		}
		if token == "" {
			return nil, ErrAgentTokenRequired
		}
		hash, err := auth.HashToken(token, pepper)
		if err != nil {
			return nil, err
		}
		hashes[agentID] = hash
	}
	return NewStaticAgentTokenHashAuthenticator(hashes, pepper)
}

func NewStaticAgentTokenHashAuthenticator(tokenHashes map[string]string, pepper string) (*StaticAgentTokenAuthenticator, error) {
	if pepper == "" {
		return nil, auth.ErrPepperRequired
	}
	cloned := make(map[string]string, len(tokenHashes))
	for agentID, tokenHash := range tokenHashes {
		if strings.TrimSpace(agentID) == "" {
			return nil, ErrAgentIDRequired
		}
		if tokenHash == "" {
			return nil, ErrAgentTokenRequired
		}
		cloned[agentID] = tokenHash
	}
	return &StaticAgentTokenAuthenticator{pepper: pepper, tokenHashes: cloned}, nil
}

func (a *StaticAgentTokenAuthenticator) VerifyAgentToken(ctx context.Context, agentID string, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil {
		return ErrAgentAuthenticatorRequired
	}
	if agentID == "" {
		return ErrAgentIDRequired
	}
	if token == "" {
		return ErrAgentTokenRequired
	}
	storedHash, ok := a.tokenHashes[agentID]
	if !ok {
		return ErrAgentTokenInvalid
	}
	verified, err := auth.VerifyToken(token, storedHash, a.pepper)
	if err != nil {
		return err
	}
	if !verified {
		return ErrAgentTokenInvalid
	}
	return nil
}
