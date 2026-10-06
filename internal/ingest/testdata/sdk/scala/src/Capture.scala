//> using scala 3
//> using dep io.sentry:sentry:8.58.0

package checkout

import io.sentry.{Sentry, SentryOptions}
import scala.util.Try

class CheckoutError(message: String, cause: Throwable) extends RuntimeException(message, cause)

def chargeCard(): Unit =
  val cause = new java.io.IOException("disk gone")
  val error = new CheckoutError("could not check out", cause)
  Sentry.captureException(error)
  throw error

def runCheckout(): Unit =
  Try(chargeCard())

@main def capture(): Unit =
  Sentry.init { (options: SentryOptions) =>
    options.setDsn(sys.env("FAULTLINE_CAPTURE_DSN"))
    options.addInAppInclude("checkout")
  }
  Seq(1).foreach(_ => runCheckout())
  Sentry.close()
