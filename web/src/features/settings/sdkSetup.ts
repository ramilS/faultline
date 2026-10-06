export interface SdkSetup {
  /** What the snippet is written in, for the card's caption. */
  readonly language: string;
  readonly code: string;
}

/**
 * The line or two that points a project's SDK at Faultline, for the platforms
 * whose snippet has been checked against the SDK's own documentation. A
 * platform without one returns null and the page falls back to the generic
 * "set the DSN" guidance: a wrong snippet is worse than none.
 */
export function sdkSetupFor(platform: string, dsn: string): SdkSetup | null {
  switch (platform) {
    case "javascript":
      return { language: "javascript", code: `Sentry.init({\n  dsn: ${JSON.stringify(dsn)},\n});` };
    case "jvm-spring":
      return { language: "application.properties", code: `sentry.dsn=${dsn}` };
    case "android":
      return {
        language: "AndroidManifest.xml",
        code: `<meta-data android:name="io.sentry.dsn" android:value="${escapeXmlAttribute(dsn)}" />`
      };
    case "ios":
      return {
        language: "swift",
        code: `SentrySDK.start { options in\n    options.dsn = ${JSON.stringify(dsn)}\n}`
      };
    case "nestjs":
      return {
        language: "instrument.ts",
        code: `import * as Sentry from "@sentry/nestjs";\n\nSentry.init({\n  dsn: ${JSON.stringify(dsn)},\n});`
      };
    case "kotlin-multiplatform":
      return {
        language: "kotlin",
        code: `import io.sentry.kotlin.multiplatform.Sentry\n\nSentry.init { options ->\n    options.dsn = ${kotlinString(dsn)}\n}`
      };
    case "scala":
      return {
        language: "scala",
        code: `// build.sbt:\n//   libraryDependencies += "io.sentry" % "sentry" % "8.58.0"\n\nimport io.sentry.{Sentry, SentryOptions}\n\nobject Main {\n  def main(args: Array[String]): Unit = {\n    Sentry.init((options: SentryOptions) => {\n      options.setDsn(${JSON.stringify(dsn)})\n      // Your own application package, so its frames are marked in-app\n      options.addInAppInclude("com.example")\n    })\n  }\n}`
      };
    case "jvm-ktor":
      return {
        language: "kotlin",
        code: `import io.sentry.Sentry\n\nSentry.init { options ->\n    options.dsn = ${kotlinString(dsn)}\n}`
      };
    case "python":
      return { language: "python", code: pythonInit(dsn) };
    case "python-django":
      return { language: "settings.py", code: pythonInit(dsn) };
    case "python-fastapi":
      return { language: "main.py", code: pythonInit(dsn) };
    case "python-flask":
      return { language: "app.py", code: pythonInit(dsn) };
    case "go":
      return { language: "go", code: goInit(dsn) };
    case "ruby":
      return { language: "ruby", code: rubyInit(dsn) };
    case "ruby-rails":
      return { language: "config/initializers/sentry.rb", code: rubyRailsInit(dsn) };
    case "php":
      return { language: "php", code: phpInit(dsn) };
    case "php-laravel":
      return { language: ".env", code: phpLaravelEnv(dsn) };
    case "php-symfony":
      return { language: ".env", code: `# composer require sentry/sentry-symfony\n\nSENTRY_DSN=${dsn}` };
    case "rust":
      return { language: "rust", code: rustInit(dsn) };
    default:
      return null;
  }
}

function escapeXmlAttribute(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function goInit(dsn: string): string {
  return `import (\n\t"log"\n\t"time"\n\n\t"github.com/getsentry/sentry-go"\n)\n\n// in main():\nerr := sentry.Init(sentry.ClientOptions{\n\tDsn: ${JSON.stringify(dsn)},\n})\nif err != nil {\n\tlog.Fatalf("sentry.Init: %s", err)\n}\ndefer sentry.Flush(2 * time.Second)`;
}

function rubyInit(dsn: string): string {
  return `Sentry.init do |config|\n  config.dsn = ${JSON.stringify(dsn)}\nend`;
}

function rubyRailsInit(dsn: string): string {
  return `# Gemfile:\n#   gem "sentry-ruby"\n#   gem "sentry-rails"\n\n${rubyInit(dsn)}`;
}

function phpInit(dsn: string): string {
  return `// composer require sentry/sentry\n\nrequire 'vendor/autoload.php';\n\n\\Sentry\\init(['dsn' => ${phpString(dsn)}]);`;
}

function phpLaravelEnv(dsn: string): string {
  return `# composer require sentry/sentry-laravel\n# bootstrap/app.php (Laravel 11+), in withExceptions(): Integration::handles($exceptions);\n\nSENTRY_LARAVEL_DSN=${dsn}`;
}

function rustInit(dsn: string): string {
  return `// Cargo.toml:\n//   sentry = "0.49"\n\n// Keep the guard alive: dropping it flushes pending events.\nlet _guard = sentry::init(\n    sentry::ClientOptions::new()\n        .dsn(${JSON.stringify(dsn)})\n        .attach_stacktrace(true),\n);`;
}

function pythonInit(dsn: string): string {
  return `import sentry_sdk\n\nsentry_sdk.init(\n    dsn=${JSON.stringify(dsn)},\n)`;
}

/** A Kotlin string literal: JSON quoting, plus `$`, which would otherwise start a template. */
function kotlinString(value: string): string {
  return JSON.stringify(value).replace(/\$/g, "\\$");
}

/** A PHP single-quoted string literal, where only the backslash and the quote need escaping. */
function phpString(value: string): string {
  return `'${value.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;
}
