package auth

// defaultPasswordVerificationLimit bounds how many Argon2id password
// verifications may run at once when nothing overrides it. Argon2id is
// deliberately memory-hard (see HashPassword's parameters), so unbounded
// concurrent verification is a resource-exhaustion vector against the
// process, not just a guessing-speed concern.
const defaultPasswordVerificationLimit = 4

// passwordVerificationGate bounds concurrent Argon2id verification. Every
// caller that checks a caller-supplied password against a stored hash —
// Service.Login and Administration.ChangeOwnPassword — must share one gate
// instance rather than each hold its own, or the two paths together can
// exceed the bound either alone would enforce. NewService and
// NewAdministration both resolve to defaultPasswordVerificationGate below
// for exactly this reason: constructed independently (as
// internal/command/run.go does), they still end up sharing one budget.
type passwordVerificationGate struct {
	slots chan struct{}
}

func newPasswordVerificationGate(limit int) *passwordVerificationGate {
	if limit <= 0 {
		limit = defaultPasswordVerificationLimit
	}
	return &passwordVerificationGate{slots: make(chan struct{}, limit)}
}

// defaultPasswordVerificationGate is the process-wide gate every Service and
// Administration shares unless ServiceOptions asks for a different one.
var defaultPasswordVerificationGate = newPasswordVerificationGate(defaultPasswordVerificationLimit)

// verify runs verify(hash, password) if a slot is immediately available,
// else reports ErrAuthenticationBusy without calling verify at all — reject
// fast rather than queue, so request latency cannot mask a saturated
// process the way waiting for a slot would.
func (g *passwordVerificationGate) verify(verify func(string, string) (bool, error), hash, password string) (bool, error) {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		return false, ErrAuthenticationBusy
	}
	return verify(hash, password)
}

// resolveVerificationGate picks the gate a new Service or Administration
// should use: an explicitly shared gate first (how a test proves two
// instances share a bound), then a fresh one sized by
// PasswordVerificationLimit for a test that wants its own small, disposable
// bound, and otherwise the shared process-wide default.
func resolveVerificationGate(options ServiceOptions) *passwordVerificationGate {
	if options.verificationGate != nil {
		return options.verificationGate
	}
	if options.PasswordVerificationLimit > 0 {
		return newPasswordVerificationGate(options.PasswordVerificationLimit)
	}
	return defaultPasswordVerificationGate
}
