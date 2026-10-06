package main

import (
	"errors"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

type CheckoutError struct{ cause error }

func (e *CheckoutError) Error() string { return "could not check out: " + e.cause.Error() }
func (e *CheckoutError) Unwrap() error { return e.cause }

func chargeCard() error {
	err := &CheckoutError{cause: errors.New("disk gone")}
	sentry.CaptureException(err)
	return err
}

func runCheckout() error {
	return chargeCard()
}

func main() {
	if err := sentry.Init(sentry.ClientOptions{Dsn: os.Getenv("FAULTLINE_CAPTURE_DSN")}); err != nil {
		panic(err)
	}
	_ = runCheckout()
	sentry.Flush(5 * time.Second)
}
