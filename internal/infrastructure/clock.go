package infrastructure

import (
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

type Clock struct{}

func NewClock() *Clock { return &Clock{} }

func (*Clock) Now() time.Time { return time.Now().UTC() }

func (*Clock) NewTimer(d time.Duration) ports.Timer { return timer{Timer: time.NewTimer(d)} }

type timer struct{ *time.Timer }

func (t timer) C() <-chan time.Time { return t.Timer.C }
