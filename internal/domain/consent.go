package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrResumeCancelled         = errors.New("resume cancelled or cancellation requested")
	ErrCapabilitiesUnverified  = errors.New("provider capabilities unverified")
	ErrObservedModeUnsupported = errors.New("cannot resume observed session: manual takeover unsupported")
	ErrConsentRequired         = errors.New("autonomous recovery consent not granted or revoked")
	ErrContextMismatch         = errors.New("execution context does not match approved consent")
)

// DigestContext computes a deterministic SHA256 digest of nonsecret ExecutionContext policy fields.
func DigestContext(ctx ExecutionContext) string {
	h := sha256.New()
	// Delimit fields safely
	h.Write([]byte(fmt.Sprintf("path:%s\nversion:%s\ncwd:%s\nprofile:%s\n",
		ctx.ExecutablePath,
		ctx.ProviderVersion,
		ctx.Cwd,
		ctx.Profile,
	)))
	return hex.EncodeToString(h.Sum(nil))
}

// CanResume validates whether a session is eligible to be resumed.
func CanResume(s Session, now time.Time, capsVerified bool, oneShot bool) error {
	// 1. Cancellation precedence & terminal state immutability
	if s.CancelRequested {
		return ErrResumeCancelled
	}
	if IsTerminal(s.State) {
		return fmt.Errorf("%w: cannot resume session in state %s", ErrTerminalStateImmutable, s.State)
	}

	// 2. Mode check: If mode is observed (manual session), observation CANNOT grant consent or authorize resume.
	if s.Mode == ModeObserved {
		return ErrObservedModeUnsupported
	}

	// 3. Capability verification: if capsVerified is false, resume is rejected.
	if !capsVerified {
		return ErrCapabilitiesUnverified
	}

	// 4. Execution context validation: current context must match its calculated digest
	computedDigest := DigestContext(s.Context)
	if s.Context.Digest != "" && s.Context.Digest != computedDigest {
		return fmt.Errorf("%w: context digest does not match computed digest", ErrContextMismatch)
	}

	// If ApprovedContextDigest is set in consent, it must match the computed digest
	if s.Consent.ApprovedContextDigest != "" && s.Consent.ApprovedContextDigest != computedDigest {
		return fmt.Errorf("%w: approved context digest %s does not match computed digest %s",
			ErrContextMismatch, s.Consent.ApprovedContextDigest, computedDigest)
	}

	// 5. Consent validation:
	// One-shot authorization permits one due attempt but never enables autonomous consent.
	// If not one-shot, autonomous consent must be enabled and unrevoked.
	if !oneShot {
		if !s.Consent.Enabled {
			return ErrConsentRequired
		}
		if s.Consent.RevokedAt != nil && !s.Consent.RevokedAt.After(now) {
			return ErrConsentRequired
		}
	}

	return nil
}
