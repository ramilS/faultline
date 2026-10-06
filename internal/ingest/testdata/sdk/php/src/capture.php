<?php

require __DIR__ . '/vendor/autoload.php';

class CheckoutError extends Exception
{
}

function chargeCard(): void
{
    try {
        throw new RuntimeException('disk gone');
    } catch (RuntimeException $cause) {
        $error = new CheckoutError('could not check out', 0, $cause);
        \Sentry\captureException($error);
        throw $error;
    }
}

function runCheckout(): void
{
    chargeCard();
}

\Sentry\init(['dsn' => getenv('FAULTLINE_CAPTURE_DSN')]);

try {
    runCheckout();
} catch (CheckoutError $error) {
}
