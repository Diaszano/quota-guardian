package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

func TestCanResume(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	approvedAt := now.Add(-1 * time.Hour)
	ctx := domain.ExecutionContext{
		ExecutablePath:  "/usr/bin/agy",
		ProviderVersion: "1.3.3",
		Cwd:             "/workspace/project",
		Profile:         "default",
	}
	digest := domain.DigestContext(ctx)
	ctx.Digest = digest

	validConsent := domain.Consent{
		Enabled:               true,
		BackgroundAllowed:     true,
		Revision:              1,
		ApprovedContextDigest: digest,
		ApprovedAt:            &approvedAt,
	}

	validSession := domain.Session{
		ID:       "session-valid",
		Provider: "agy",
		Mode:     domain.ModeManaged,
		State:    domain.WaitingForReset,
		Revision: 1,
		Context:  ctx,
		Consent:  validConsent,
	}

	// 1. Happy path: managed session with valid consent, matching digest, verified caps
	err := domain.CanResume(validSession, now, true, false)
	if err != nil {
		t.Fatalf("expected valid session to resume successfully, got: %v", err)
	}

	// 2. Cancellation precedence: CancelRequested true
	cancelledSession := validSession
	cancelledSession.CancelRequested = true
	err = domain.CanResume(cancelledSession, now, true, false)
	if !errors.Is(err, domain.ErrResumeCancelled) {
		t.Fatalf("expected ErrResumeCancelled, got: %v", err)
	}

	// Terminal state cannot resume
	terminalSession := validSession
	terminalSession.State = domain.Completed
	err = domain.CanResume(terminalSession, now, true, false)
	if !errors.Is(err, domain.ErrTerminalStateImmutable) {
		t.Fatalf("expected ErrTerminalStateImmutable for completed session, got: %v", err)
	}

	// 3. Capabilities unverified: capsVerified = false
	err = domain.CanResume(validSession, now, false, false)
	if !errors.Is(err, domain.ErrCapabilitiesUnverified) {
		t.Fatalf("expected ErrCapabilitiesUnverified, got: %v", err)
	}

	// 4. Consent disabled
	noConsentSession := validSession
	noConsentSession.Consent.Enabled = false
	err = domain.CanResume(noConsentSession, now, true, false)
	if !errors.Is(err, domain.ErrConsentRequired) {
		t.Fatalf("expected ErrConsentRequired, got: %v", err)
	}

	// One-shot permits one due attempt even if autonomous consent is disabled
	err = domain.CanResume(noConsentSession, now, true, true)
	if err != nil {
		t.Fatalf("expected oneShot to allow resume when consent is disabled, got: %v", err)
	}

	// But one-shot STILL enforces context digest, cancellation, mode, caps
	noConsentBadCtx := noConsentSession
	noConsentBadCtx.Context.Cwd = "/other/path"
	noConsentBadCtx.Context.Digest = domain.DigestContext(noConsentBadCtx.Context)
	err = domain.CanResume(noConsentBadCtx, now, true, true)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch even with oneShot, got: %v", err)
	}

	// 5. Consent revoked
	revokedAt := now.Add(-5 * time.Minute)
	revokedConsentSession := validSession
	revokedConsentSession.Consent.RevokedAt = &revokedAt
	err = domain.CanResume(revokedConsentSession, now, true, false)
	if !errors.Is(err, domain.ErrConsentRequired) {
		t.Fatalf("expected ErrConsentRequired when consent is revoked, got: %v", err)
	}
}

func TestConsentCannotBeGrantedByObservation(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ctx := domain.ExecutionContext{
		ExecutablePath:  "/usr/bin/agy",
		ProviderVersion: "1.3.3",
		Cwd:             "/workspace/project",
		Profile:         "default",
	}
	ctx.Digest = domain.DigestContext(ctx)

	// ModeObserved cannot be resumed, even if consent is enabled and oneShot is true
	observedSession := domain.Session{
		ID:       "session-observed",
		Provider: "agy",
		Mode:     domain.ModeObserved,
		State:    domain.WaitingForReset,
		Revision: 1,
		Context:  ctx,
		Consent: domain.Consent{
			Enabled:               true,
			BackgroundAllowed:     true,
			Revision:              1,
			ApprovedContextDigest: ctx.Digest,
		},
	}

	err := domain.CanResume(observedSession, now, true, false)
	if !errors.Is(err, domain.ErrObservedModeUnsupported) {
		t.Fatalf("expected ErrObservedModeUnsupported for observed session, got: %v", err)
	}

	// One-shot cannot override observed session prohibition
	err = domain.CanResume(observedSession, now, true, true)
	if !errors.Is(err, domain.ErrObservedModeUnsupported) {
		t.Fatalf("expected ErrObservedModeUnsupported even with oneShot, got: %v", err)
	}
}

func TestContextChangeInvalidatesConsent(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	approvedAt := now.Add(-1 * time.Hour)
	ctx := domain.ExecutionContext{
		ExecutablePath:  "/usr/bin/agy",
		ProviderVersion: "1.3.3",
		Cwd:             "/workspace/project",
		Profile:         "default",
	}
	originalDigest := domain.DigestContext(ctx)
	ctx.Digest = originalDigest

	session := domain.Session{
		ID:       "session-ctx-change",
		Provider: "agy",
		Mode:     domain.ModeManaged,
		State:    domain.WaitingForReset,
		Revision: 1,
		Context:  ctx,
		Consent: domain.Consent{
			Enabled:               true,
			BackgroundAllowed:     true,
			Revision:              1,
			ApprovedContextDigest: originalDigest,
			ApprovedAt:            &approvedAt,
		},
	}

	// 1. Changing ExecutablePath
	changedPath := session
	changedPath.Context.ExecutablePath = "/usr/local/bin/agy"
	changedPath.Context.Digest = domain.DigestContext(changedPath.Context)
	err := domain.CanResume(changedPath, now, true, false)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch when ExecutablePath changed, got: %v", err)
	}

	// 2. Changing ProviderVersion
	changedVersion := session
	changedVersion.Context.ProviderVersion = "1.4.0"
	changedVersion.Context.Digest = domain.DigestContext(changedVersion.Context)
	err = domain.CanResume(changedVersion, now, true, false)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch when ProviderVersion changed, got: %v", err)
	}

	// 3. Changing Cwd
	changedCwd := session
	changedCwd.Context.Cwd = "/workspace/other"
	changedCwd.Context.Digest = domain.DigestContext(changedCwd.Context)
	err = domain.CanResume(changedCwd, now, true, false)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch when Cwd changed, got: %v", err)
	}

	// 4. Changing Profile
	changedProfile := session
	changedProfile.Context.Profile = "custom"
	changedProfile.Context.Digest = domain.DigestContext(changedProfile.Context)
	err = domain.CanResume(changedProfile, now, true, false)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch when Profile changed, got: %v", err)
	}

	// 5. Tampered Digest: context fields match original, but Context.Digest is corrupt
	tamperedDigest := session
	tamperedDigest.Context.Digest = "corrupt-digest"
	err = domain.CanResume(tamperedDigest, now, true, false)
	if !errors.Is(err, domain.ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch when Digest does not match recalculated digest, got: %v", err)
	}
}
