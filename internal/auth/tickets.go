package auth

import (
	"errors"
	"time"
)

type TicketRole string

const (
	TicketRoleSenderAgent      TicketRole = "sender_agent"
	TicketRoleReceivingAgent   TicketRole = "receiving_agent"
	TicketRoleBrowserRecipient TicketRole = "browser_recipient"
)

var (
	ErrTicketTransferRequired = errors.New("ticket transfer id is required")
	ErrTicketRoleRequired     = errors.New("ticket role is required")
	ErrTicketRoleUnsupported  = errors.New("unsupported ticket role")
	ErrTicketTTLNotPositive   = errors.New("ticket ttl must be positive")
	ErrTicketNowRequired      = errors.New("ticket verification time is required")
	ErrTicketBindingMismatch  = errors.New("ticket binding mismatch")
	ErrTicketExpired          = errors.New("ticket expired")
	ErrTicketInvalid          = errors.New("ticket invalid")
)

type TicketIssuer struct {
	Pepper string
	Now    func() time.Time
}

type TicketRequest struct {
	TransferID string
	Role       TicketRole
	TTL        time.Duration
}

type IssuedTicket struct {
	Raw        string
	Hash       string
	TransferID string
	Role       TicketRole
	ExpiresAt  time.Time
}

type VerifyTicketRequest struct {
	Raw        string
	Hash       string
	Pepper     string
	TransferID string
	Role       TicketRole
	ExpiresAt  time.Time
	Now        time.Time
}

type TicketClaims struct {
	TransferID string
	Role       TicketRole
}

func (i TicketIssuer) Issue(req TicketRequest) (IssuedTicket, error) {
	if req.TransferID == "" {
		return IssuedTicket{}, ErrTicketTransferRequired
	}
	if req.Role == "" {
		return IssuedTicket{}, ErrTicketRoleRequired
	}
	if !isSupportedTicketRole(req.Role) {
		return IssuedTicket{}, ErrTicketRoleUnsupported
	}
	if req.TTL <= 0 {
		return IssuedTicket{}, ErrTicketTTLNotPositive
	}
	now := time.Now().UTC()
	if i.Now != nil {
		now = i.Now()
	}
	raw, err := GenerateToken("tkt", 32)
	if err != nil {
		return IssuedTicket{}, err
	}
	hash, err := HashToken(ticketBindingRaw(raw, req.TransferID, req.Role), i.Pepper)
	if err != nil {
		return IssuedTicket{}, err
	}
	return IssuedTicket{
		Raw:        raw,
		Hash:       hash,
		TransferID: req.TransferID,
		Role:       req.Role,
		ExpiresAt:  now.Add(req.TTL),
	}, nil
}

func VerifyTicket(req VerifyTicketRequest) (TicketClaims, error) {
	if req.TransferID == "" {
		return TicketClaims{}, ErrTicketTransferRequired
	}
	if req.Role == "" {
		return TicketClaims{}, ErrTicketRoleRequired
	}
	if !isSupportedTicketRole(req.Role) {
		return TicketClaims{}, ErrTicketRoleUnsupported
	}
	if req.Now.IsZero() {
		return TicketClaims{}, ErrTicketNowRequired
	}
	if !req.Now.Before(req.ExpiresAt) {
		return TicketClaims{}, ErrTicketExpired
	}
	if req.Raw == "" || len(req.Raw) < len("tkt_") || req.Raw[:len("tkt_")] != "tkt_" {
		return TicketClaims{}, ErrTicketInvalid
	}
	ok, err := VerifyToken(ticketBindingRaw(req.Raw, req.TransferID, req.Role), req.Hash, req.Pepper)
	if err != nil {
		return TicketClaims{}, ErrTicketInvalid
	}
	if !ok {
		return TicketClaims{}, ErrTicketBindingMismatch
	}
	return TicketClaims{TransferID: req.TransferID, Role: req.Role}, nil
}

func ticketBindingRaw(raw string, transferID string, role TicketRole) string {
	return string(role) + ":" + transferID + ":" + raw
}

func isSupportedTicketRole(role TicketRole) bool {
	switch role {
	case TicketRoleSenderAgent, TicketRoleReceivingAgent, TicketRoleBrowserRecipient:
		return true
	default:
		return false
	}
}
