import assert from "node:assert/strict";
import { test } from "node:test";

import { sdkSetupFor } from "./sdkSetup.ts";

const DSN = "http://abc123@localhost:8080/1";

test("javascript: initialises the SDK with the DSN", () => {
  const setup = sdkSetupFor("javascript", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "javascript");
  assert.match(setup.code, /Sentry\.init\(\{/);
  assert.ok(setup.code.includes(`dsn: "${DSN}"`));
});

test("jvm-spring: sets the sentry.dsn property", () => {
  const setup = sdkSetupFor("jvm-spring", DSN);

  assert.ok(setup);
  assert.equal(setup.code, `sentry.dsn=${DSN}`);
});

test("android: declares the DSN as manifest meta-data", () => {
  const setup = sdkSetupFor("android", DSN);

  assert.ok(setup);
  assert.ok(setup.code.includes('android:name="io.sentry.dsn"'));
  assert.ok(setup.code.includes(`android:value="${DSN}"`));
});

test("ios: starts the SDK with the DSN", () => {
  const setup = sdkSetupFor("ios", DSN);

  assert.ok(setup);
  assert.ok(setup.code.includes("SentrySDK.start"));
  assert.ok(setup.code.includes(`options.dsn = "${DSN}"`));
});

test("nestjs: initialises the SDK from its instrument file with the DSN", () => {
  const setup = sdkSetupFor("nestjs", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "instrument.ts");
  assert.ok(setup.code.includes('import * as Sentry from "@sentry/nestjs";'));
  assert.ok(setup.code.includes(`dsn: "${DSN}"`));
});

test("kotlin-multiplatform: initialises the shared SDK with the DSN", () => {
  const setup = sdkSetupFor("kotlin-multiplatform", DSN);

  assert.ok(setup);
  assert.ok(setup.code.includes("import io.sentry.kotlin.multiplatform.Sentry"));
  assert.ok(setup.code.includes(`options.dsn = "${DSN}"`));
});

test("jvm-ktor: initialises the JVM SDK with the DSN", () => {
  const setup = sdkSetupFor("jvm-ktor", DSN);

  assert.ok(setup);
  assert.ok(setup.code.includes("import io.sentry.Sentry"));
  assert.ok(setup.code.includes(`options.dsn = "${DSN}"`));
});

test("kotlin: a dollar sign in a DSN cannot start a string template", () => {
  for (const platform of ["kotlin-multiplatform", "jvm-ktor"]) {
    const setup = sdkSetupFor(platform, "http://k@h/${System.exit(1)}");

    assert.ok(setup);
    assert.ok(setup.code.includes("\\${System.exit(1)}"), setup.code);
  }
});

test("platforms without a checked snippet get none, rather than a guess", () => {
  for (const platform of ["other", "cobol"]) {
    assert.equal(sdkSetupFor(platform, DSN), null, platform);
  }
});

test("quotes in a DSN cannot break out of the string they are placed in", () => {
  const hostile = 'http://k@h/1"; alert(1); //';

  const setup = sdkSetupFor("javascript", hostile);

  assert.ok(setup);
  // The quote arrives escaped, so the string literal is still one literal.
  assert.ok(setup.code.includes('\\"; alert(1)'), setup.code);
  assert.equal(JSON.parse(setup.code.split("dsn: ")[1].split(",\n")[0]), hostile);
});

test("python: initialises sentry_sdk with the DSN", () => {
  const setup = sdkSetupFor("python", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "python");
  assert.match(setup.code, /sentry_sdk\.init\(/);
  assert.ok(setup.code.includes(`dsn="${DSN}"`));
});

test("python frameworks: reuse the same init call in the framework's settings", () => {
  for (const platform of ["python-django", "python-fastapi", "python-flask"]) {
    const setup = sdkSetupFor(platform, DSN);

    assert.ok(setup, platform);
    assert.ok(setup.code.includes(`dsn="${DSN}"`), platform);
  }
});

test("go: initialises the SDK with the DSN", () => {
  const setup = sdkSetupFor("go", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "go");
  assert.match(setup.code, /sentry\.Init\(sentry\.ClientOptions\{/);
  assert.ok(setup.code.includes(`Dsn: "${DSN}"`));
  for (const path of ['"log"', '"time"', '"github.com/getsentry/sentry-go"']) {
    assert.ok(setup.code.includes(path), `missing import ${path}`);
  }
});

test("ruby: configures Sentry with the DSN", () => {
  const setup = sdkSetupFor("ruby", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "ruby");
  assert.match(setup.code, /Sentry\.init do \|config\|/);
  assert.ok(setup.code.includes(`config.dsn = "${DSN}"`));
});

test("ruby-rails: configures Sentry from an initializer", () => {
  const setup = sdkSetupFor("ruby-rails", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "config/initializers/sentry.rb");
  assert.ok(setup.code.includes('gem "sentry-ruby"'));
  assert.ok(setup.code.includes('gem "sentry-rails"'));
  assert.ok(setup.code.includes(`config.dsn = "${DSN}"`));
});

test("php: requires the SDK and initialises it with the DSN", () => {
  const setup = sdkSetupFor("php", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "php");
  assert.ok(setup.code.includes("composer require sentry/sentry"));
  assert.ok(setup.code.includes("require 'vendor/autoload.php';"));
  assert.ok(setup.code.includes(`\\Sentry\\init(['dsn' => '${DSN}']);`));
});

test("php: escapes a quote and a backslash in the DSN", () => {
  const setup = sdkSetupFor("php", "http://k@h/1'\\");

  assert.ok(setup);
  assert.ok(setup.code.includes(`\\Sentry\\init(['dsn' => 'http://k@h/1\\'\\\\']);`));
});

test("php-laravel: installs the Laravel package and sets the DSN in the environment file", () => {
  const setup = sdkSetupFor("php-laravel", DSN);

  assert.ok(setup);
  assert.equal(setup.language, ".env");
  assert.ok(setup.code.includes("composer require sentry/sentry-laravel"));
  assert.ok(setup.code.includes("Integration::handles($exceptions);"));
  assert.ok(setup.code.includes("Laravel 11+"));
  assert.ok(setup.code.includes(`SENTRY_LARAVEL_DSN=${DSN}`));
});

test("php-symfony: installs the Symfony bundle and sets the DSN in the environment file", () => {
  const setup = sdkSetupFor("php-symfony", DSN);

  assert.ok(setup);
  assert.equal(setup.language, ".env");
  assert.ok(setup.code.includes("composer require sentry/sentry-symfony"));
  assert.ok(setup.code.includes(`SENTRY_DSN=${DSN}`));
});

test("rust: requires the sentry crate and initialises it with the DSN, keeping the guard alive", () => {
  const setup = sdkSetupFor("rust", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "rust");
  assert.ok(setup.code.includes('sentry = "0.49"'));
  assert.match(setup.code, /let _guard = sentry::init\(/);
  assert.ok(setup.code.includes(`.dsn(${JSON.stringify(DSN)})`));
});

test("scala: requires the JVM SDK artifact and initialises it with the DSN", () => {
  const setup = sdkSetupFor("scala", DSN);

  assert.ok(setup);
  assert.equal(setup.language, "scala");
  assert.ok(setup.code.includes('libraryDependencies += "io.sentry" % "sentry" % "8.58.0"'));
  assert.match(setup.code, /Sentry\.init\(/);
  assert.ok(setup.code.includes(`setDsn(${JSON.stringify(DSN)})`));
  assert.ok(setup.code.includes('options.addInAppInclude("com.example")'));
  assert.ok(setup.code.includes("object Main {"), "a bare top-level statement compiles only on Scala 3");
  assert.ok(setup.code.includes("def main(args: Array[String]): Unit = {"));
});
