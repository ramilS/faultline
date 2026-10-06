require "sentry-ruby"

class CheckoutError < StandardError; end

def charge_card
  begin
    raise IOError, "disk gone"
  rescue IOError
    raise CheckoutError, "could not check out"
  end
rescue CheckoutError => error
  Sentry.capture_exception(error)
  raise
end

def run_checkout
  charge_card
end

Sentry.init do |config|
  config.dsn = ENV.fetch("FAULTLINE_CAPTURE_DSN")
  config.background_worker_threads = 0
end

begin
  run_checkout
rescue CheckoutError
  nil
end
