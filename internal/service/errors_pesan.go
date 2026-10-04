package service

// pesan carries a message written for the person at the till while still
// matching its sentinel with errors.Is.
//
// fmt.Errorf("%w: ...", ErrInsufficientStock, ...) would prepend the
// sentinel's own text, and that text is English on purpose — fifo/errors.go
// says the domain's messages are code and must never be shown to a cashier.
// Wrapping this way keeps errors.Is working and leaves Error() as the sentence
// we actually wrote.
type pesan struct {
	sentinel error
	msg      string
}

func (p *pesan) Error() string { return p.msg }
func (p *pesan) Unwrap() error { return p.sentinel }

// pesankan wraps sentinel with a user-facing Indonesian message.
func pesankan(sentinel error, msg string) error {
	return &pesan{sentinel: sentinel, msg: msg}
}
