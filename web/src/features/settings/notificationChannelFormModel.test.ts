import assert from "node:assert/strict";
import { test } from "node:test";

import type { NotificationChannel } from "../../api.ts";
import {
  channelFormErrors,
  channelHealth,
  hasChannelFormError,
  initialChannelFormValues,
  parseEnvironments,
  toChannelInput,
  type ChannelFormValues
} from "./notificationChannelFormModel.ts";

const stored: NotificationChannel = {
  id: 3,
  name: "Ops",
  kind: "telegram",
  target: "-100",
  has_secret: true,
  proxy: "socks5://bot:xxxxx@proxy.example.com:1080",
  enabled: true,
  notify_new: true,
  notify_regression: false,
  environments: ["production", "staging"],
  min_level: "warning",
  last_success_at: null,
  last_error: null,
  last_error_at: null,
  created_at: "2026-10-15T08:00:00Z"
};

function values(changes: Partial<ChannelFormValues>): ChannelFormValues {
  return { ...initialChannelFormValues(null), name: "Ops", target: "-100", secret: "123:abc", ...changes };
}

test("editing starts from the stored channel with a blank secret", () => {
  assert.deepEqual(initialChannelFormValues(stored), {
    name: "Ops",
    kind: "telegram",
    target: "-100",
    secret: "",
    proxy: "",
    removeProxy: false,
    enabled: true,
    notifyNew: true,
    notifyRegression: false,
    environments: "production, staging",
    minLevel: "warning"
  });
});

test("environments split on commas and newlines, trimmed and deduplicated", () => {
  assert.deepEqual(parseEnvironments(" production,staging\nproduction ,, \n"), ["production", "staging"]);
  assert.deepEqual(parseEnvironments("   "), []);
});

test("toChannelInput sends the secret on create, even blank, so the server can reject it", () => {
  assert.equal(toChannelInput(values({ secret: "" }), false).secret, "");
});

test("toChannelInput omits a blank secret on edit so the stored one is kept", () => {
  assert.equal("secret" in toChannelInput(values({ secret: "  " }), true), false);
  assert.equal(toChannelInput(values({ secret: " 999:new " }), true).secret, "999:new");
});

test("toChannelInput trims and maps every field to its wire name", () => {
  assert.deepEqual(toChannelInput(values({ name: " Ops ", target: " -100 ", environments: "prod" }), false), {
    name: "Ops",
    kind: "telegram",
    target: "-100",
    secret: "123:abc",
    enabled: true,
    notify_new: true,
    notify_regression: true,
    environments: ["prod"],
    min_level: "error"
  });
});

test("toChannelInput sends a proxy only when set, and an empty one to remove it", () => {
  const cases: ReadonlyArray<[string, Partial<ChannelFormValues>, boolean, string | undefined]> = [
    ["blank on create", { proxy: " " }, false, undefined],
    ["blank on edit keeps the stored one", { proxy: "" }, true, undefined],
    ["a new proxy, trimmed", { proxy: " socks5://bot:pw@proxy.example.com:1080 " }, true, "socks5://bot:pw@proxy.example.com:1080"],
    ["removal wins over typed text", { proxy: "socks5://proxy.example.com:1080", removeProxy: true }, true, ""],
    ["never for a webhook", { kind: "webhook", target: "https://hooks.example/x", proxy: "socks5://proxy.example.com:1080" }, false, undefined]
  ];
  for (const [name, changes, isEditing, want] of cases) {
    const input = toChannelInput(values(changes), isEditing);
    assert.equal(input.proxy, want, name);
    assert.equal("proxy" in input, want !== undefined, name);
  }
});

const errorCases: ReadonlyArray<[string, ChannelFormValues, boolean, keyof ReturnType<typeof channelFormErrors>]> = [
  ["blank name", values({ name: " " }), false, "name"],
  ["no trigger", values({ notifyNew: false, notifyRegression: false }), false, "triggers"],
  ["telegram without chat", values({ target: "" }), false, "target"],
  ["telegram create without token", values({ secret: "" }), false, "secret"],
  ["an MTProto proxy link", values({ proxy: "tg://proxy?server=proxy.example.com&port=443&secret=abc" }), false, "proxy"],
  ["a proxy without a scheme", values({ proxy: "proxy.example.com:1080" }), false, "proxy"],
  ["webhook without a scheme", values({ kind: "webhook", target: "hooks.example/x" }), false, "target"]
];

for (const [name, input, isEditing, field] of errorCases) {
  test(`channelFormErrors flags ${name}`, () => {
    const errors = channelFormErrors(input, isEditing);
    assert.notEqual(errors[field], null);
    assert.equal(hasChannelFormError(errors), true);
  });
}

test("channelFormErrors accepts a telegram edit without a new token and a valid webhook", () => {
  assert.equal(hasChannelFormError(channelFormErrors(values({ secret: "" }), true)), false);
  for (const proxy of ["socks5://bot:p@ss@proxy.example.com:1080", "socks5h://proxy:1080", "http://10.0.0.5:3128", "https://proxy.example.com/"]) {
    assert.equal(channelFormErrors(values({ proxy }), false).proxy, null, proxy);
  }
  assert.equal(hasChannelFormError(channelFormErrors(values({ kind: "webhook", target: "https://hooks.example/x", secret: "" }), false)), false);
});

test("channelHealth", () => {
  const cases: ReadonlyArray<[string, Partial<NotificationChannel>, ReturnType<typeof channelHealth>]> = [
    ["never delivered", {}, { state: "idle" }],
    ["delivered", { last_success_at: "2026-10-15T09:00:00Z" }, { state: "ok", at: "2026-10-15T09:00:00Z" }],
    [
      "failing with no success yet",
      { last_error: "webhook: HTTP 500", last_error_at: "2026-10-15T09:00:00Z" },
      { state: "failing", at: "2026-10-15T09:00:00Z", message: "webhook: HTTP 500" }
    ],
    [
      "an error newer than the last success",
      { last_success_at: "2026-10-15T09:00:00Z", last_error: "webhook: HTTP 500", last_error_at: "2026-10-15T10:00:00Z" },
      { state: "failing", at: "2026-10-15T10:00:00Z", message: "webhook: HTTP 500" }
    ],
    [
      "an error since recovered from",
      { last_success_at: "2026-10-15T11:00:00Z", last_error: "webhook: HTTP 500", last_error_at: "2026-10-15T10:00:00Z" },
      { state: "ok", at: "2026-10-15T11:00:00Z" }
    ]
  ];
  for (const [name, changes, want] of cases) {
    assert.deepEqual(channelHealth({ ...stored, ...changes }), want, name);
  }
});
