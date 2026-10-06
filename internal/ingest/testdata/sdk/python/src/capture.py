import os

import sentry_sdk


class CheckoutError(Exception):
    pass


def charge_card():
    try:
        raise OSError("disk gone")
    except OSError as cause:
        raise CheckoutError("could not check out") from cause


def run_checkout():
    charge_card()


def main():
    sentry_sdk.init(dsn=os.environ["FAULTLINE_CAPTURE_DSN"])
    try:
        run_checkout()
    except CheckoutError as error:
        sentry_sdk.capture_exception(error)
    sentry_sdk.flush()


main()
