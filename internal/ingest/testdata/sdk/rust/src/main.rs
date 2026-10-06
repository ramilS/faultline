use std::env;
use std::fmt;

#[derive(Debug)]
struct CheckoutError {
    cause: std::io::Error,
}

impl fmt::Display for CheckoutError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "could not check out")
    }
}

impl std::error::Error for CheckoutError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        Some(&self.cause)
    }
}

#[inline(never)]
fn charge_card() -> Result<(), CheckoutError> {
    let error = CheckoutError {
        cause: std::io::Error::new(std::io::ErrorKind::Other, "disk gone"),
    };
    sentry::capture_error(&error);
    Err(error)
}

#[inline(never)]
fn run_checkout() -> Result<(), CheckoutError> {
    charge_card()
}

fn main() {
    let dsn = env::var("FAULTLINE_CAPTURE_DSN").expect("FAULTLINE_CAPTURE_DSN");
    let _guard = sentry::init(
        sentry::ClientOptions::new()
            .dsn(&dsn)
            .attach_stacktrace(true),
    );
    let _ = run_checkout();
}
