import { test } from "node:test";
import assert from "node:assert/strict";

import { ApiError, createApi, isMergedIssue, type FetchLike } from "./api.ts";

interface RecordedCall {
  url: string;
  init: RequestInit;
}

function recordingFetch(response: Response): { fetch: FetchLike; calls: RecordedCall[] } {
  const calls: RecordedCall[] = [];
  const fetch: FetchLike = async (url, init = {}) => {
    calls.push({ url, init });
    return response;
  };
  return { fetch, calls };
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("listProjects issues a same-origin GET and returns the projects array", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { projects: [{ id: 1, slug: "web", name: "Web", platform: "javascript", created_at: "2026-07-13T08:00:00Z" }] }),
  );
  const api = createApi(fetch);

  const projects = await api.listProjects();

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/v1/projects");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal(projects[0].slug, "web");
});

test("createProject posts JSON with same-origin credentials", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(201, { project: { id: 2, slug: "api", name: "API", platform: "jvm-spring", created_at: "2026-07-13T08:00:00Z" } }),
  );
  const api = createApi(fetch);

  const created = await api.createProject({ slug: "api", name: "API", platform: "jvm-spring" });

  assert.equal(calls[0].url, "/api/v1/projects");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal((calls[0].init.headers as Record<string, string>)["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { slug: "api", name: "API", platform: "jvm-spring" });
  assert.equal(created.slug, "api");
});

test("createProject sends each new platform as its literal wire value", async () => {
  for (const platform of ["kotlin-multiplatform", "nestjs", "android", "ios"] as const) {
    const { fetch, calls } = recordingFetch(
      jsonResponse(201, { project: { id: 3, slug: "app", name: "App", platform, created_at: "2026-07-13T08:00:00Z" } }),
    );
    const api = createApi(fetch);

    await api.createProject({ slug: "app", name: "App", platform });

    assert.deepEqual(JSON.parse(calls[0].init.body as string), { slug: "app", name: "App", platform });
  }
});

test("updateProject patches only name and platform at the escaped slug", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { project: { id: 2, slug: "api", name: "Billing API", platform: "go", created_at: "2026-07-13T08:00:00Z" } }),
  );
  const api = createApi(fetch);

  const updated = await api.updateProject("a/b", { name: "Billing API", platform: "go" });

  assert.equal(calls[0].url, "/api/v1/projects/a%2Fb");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { name: "Billing API", platform: "go" });
  assert.equal(updated.name, "Billing API");
  assert.equal(updated.platform, "go");
});

test("createKey returns raw key material only at the top level", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(201, {
      key: { id: 5, project_id: 2, public_key: "public-5", rate_limit_rps: 200, rate_limit_burst: 1000, revoked_at: null },
      raw_key: "a".repeat(64),
    }),
  );
  const api = createApi(fetch);

  const created = await api.createKey("api", { rate_limit_rps: 200, rate_limit_burst: 1000 });

  assert.equal(calls[0].url, "/api/v1/projects/api/keys");
  assert.equal(created.raw_key, "a".repeat(64));
  assert.equal("raw_key" in created.key, false);
});

test("listKeys returns persisted keys that never carry raw material", async () => {
  const { fetch } = recordingFetch(
    jsonResponse(200, { keys: [{ id: 5, project_id: 2, public_key: "public-5", rate_limit_rps: 200, rate_limit_burst: 1000, revoked_at: null }] }),
  );
  const api = createApi(fetch);

  const keys = await api.listKeys("api");

  assert.equal(keys.length, 1);
  assert.equal("raw_key" in keys[0], false);
});

test("revokeKey posts to the revoke route and resolves on 204", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  await api.revokeKey("api", 5);

  assert.equal(calls[0].url, "/api/v1/projects/api/keys/5/revoke");
  assert.equal(calls[0].init.method, "POST");
});

test("non-ok responses raise an ApiError carrying the status and message", async () => {
  const { fetch } = recordingFetch(jsonResponse(409, { error: "project already exists" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.createProject({ slug: "api", name: "API", platform: "other" }),
    (error: unknown) => error instanceof ApiError && error.status === 409 && error.message === "project already exists",
  );
});

test("listIssues builds a same-origin GET with repeated multi-value filters and tags", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { issues: [], next_cursor: null }),
  );
  const api = createApi(fetch);

  await api.listIssues("web", {
    statuses: ["unresolved", "ignored"],
    levels: ["error", "fatal"],
    environments: ["prod"],
    tags: ["tenant:acme", "browser:chrome"],
    sort: "times_seen",
    limit: 25
  });

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.pathname, "/api/v1/projects/web/issues");
  assert.deepEqual(url.searchParams.getAll("status"), ["unresolved", "ignored"]);
  assert.deepEqual(url.searchParams.getAll("level"), ["error", "fatal"]);
  assert.equal(url.searchParams.get("environment"), "prod");
  assert.equal(url.searchParams.get("sort"), "times_seen");
  assert.deepEqual(url.searchParams.getAll("tag"), ["tenant:acme", "browser:chrome"]);
  assert.equal(calls[0].init.credentials, "same-origin");
});

test("listIssues without a project reads the every-project list", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { issues: [], next_cursor: null }));
  const api = createApi(fetch);

  await api.listIssues(null, { statuses: ["unresolved"] });

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.pathname, "/api/v1/issues");
  assert.deepEqual(url.searchParams.getAll("status"), ["unresolved"]);
});

test("listIssues forwards assigned and the time window", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { issues: [], next_cursor: null }));
  const api = createApi(fetch);

  await api.listIssues("web", { assigned: "me", statsPeriod: "30d" });

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.searchParams.get("assigned"), "me");
  assert.equal(url.searchParams.get("statsPeriod"), "30d");
});

test("listIssues omits assigned entirely rather than sending it empty", async () => {
  // A dropdown bound to this param commonly emits the key with an empty
  // value on "no filter"; the API answers ?assigned= with 400.
  const { fetch, calls } = recordingFetch(jsonResponse(200, { issues: [], next_cursor: null }));
  const api = createApi(fetch);

  await api.listIssues("web", { assigned: "" });

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.searchParams.has("assigned"), false);
});

test("listTagKeys returns a project's tag vocabulary", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { tags: [{ key: "environment", value_count: 2, times_seen: 40 }] }),
  );
  const api = createApi(fetch);

  const keys = await api.listTagKeys("web");

  assert.equal(calls[0].url, "/api/v1/projects/web/tags");
  assert.deepEqual(keys, [{ key: "environment", value_count: 2, times_seen: 40 }]);
});

test("listTagValues forwards the prefix query and decodes values", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { values: [{ value: "production", times_seen: 10, first_seen: "2026-07-01T00:00:00Z", last_seen: "2026-07-02T00:00:00Z" }] }),
  );
  const api = createApi(fetch);

  const values = await api.listTagValues("web", "environment", "prod");

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.pathname, "/api/v1/projects/web/tags/environment/values");
  assert.equal(url.searchParams.get("query"), "prod");
  assert.equal(values[0].value, "production");
});

test("listTagValues omits the query parameter rather than sending it empty", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { values: [] }));
  const api = createApi(fetch);

  await api.listTagValues("web", "environment");

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.searchParams.has("query"), false);
});

test("listIssueEvents requests the newest end with no query string at all", async () => {
  // The server already defaults to newest; sending order=newest would only
  // create a second spelling of the same request.
  const unspecified = recordingFetch(jsonResponse(200, { events: [], next_cursor: null }));
  const explicit = recordingFetch(jsonResponse(200, { events: [], next_cursor: null }));

  await createApi(unspecified.fetch).listIssueEvents(5);
  await createApi(explicit.fetch).listIssueEvents(5, { order: "newest" });

  assert.equal(unspecified.calls[0].url, "/api/v1/issues/5/events");
  assert.equal(explicit.calls[0].url, "/api/v1/issues/5/events");
});

test("listIssueEvents forwards order=oldest", async () => {
  // Without this the server silently answers newest-first while the pager
  // still labels the rows "Oldest" — wrong data under a green gate.
  const { fetch, calls } = recordingFetch(jsonResponse(200, { events: [], next_cursor: null }));
  const api = createApi(fetch);

  await api.listIssueEvents(5, { order: "oldest" });

  const url = new URL(calls[0].url, "http://x.test");
  assert.equal(url.pathname, "/api/v1/issues/5/events");
  assert.equal(url.searchParams.get("order"), "oldest");
});

test("listIssueEvents sends a cursor alongside the order that produced it", async () => {
  const oldest = recordingFetch(jsonResponse(200, { events: [], next_cursor: null }));
  const defaulted = recordingFetch(jsonResponse(200, { events: [], next_cursor: null }));

  await createApi(oldest.fetch).listIssueEvents(5, { cursor: "c1", order: "oldest" });
  await createApi(defaulted.fetch).listIssueEvents(5, { cursor: "c2" });

  const oldestUrl = new URL(oldest.calls[0].url, "http://x.test");
  assert.equal(oldestUrl.searchParams.get("cursor"), "c1");
  assert.equal(oldestUrl.searchParams.get("order"), "oldest");

  const defaultedUrl = new URL(defaulted.calls[0].url, "http://x.test");
  assert.equal(defaultedUrl.searchParams.get("cursor"), "c2");
  assert.equal(defaultedUrl.searchParams.has("order"), false);
});

test("listIssueUsers requests the users facet with no query string and returns the whole envelope", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      users: [{ key: "email", value: "a@example.com", times_seen: 4, last_seen: "2026-08-01T00:00:00Z" }],
      total: 4,
      saturated: false,
    }),
  );
  const api = createApi(fetch);

  const facet = await api.listIssueUsers(5);

  assert.equal(calls[0].url, "/api/v1/issues/5/users");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  // The full envelope, not just `users`: `total` and `saturated` are read
  // independently of the returned rows (see the ruling against "showing N
  // of total" arithmetic), so a caller needs all three fields.
  assert.equal(facet.total, 4);
  assert.equal(facet.saturated, false);
  assert.equal(facet.users[0].key, "email");
  assert.equal(facet.users[0].value, "a@example.com");
});

test("patchIssue sends a PATCH with the changed fields", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { issue: { id: 5, title: "Boom", culprit: "", level: "error", status: "resolved", assignee: null, times_seen: 1, first_seen: "2026-07-16T00:00:00Z", last_seen: "2026-07-16T00:00:00Z", counts_24h: [], counts_14d: [] }, environments: [], releases: [], tags: [], regression: null }),
  );
  const api = createApi(fetch);

  const detail = await api.patchIssue(5, { status: "resolved" });

  assert.equal(calls[0].url, "/api/v1/issues/5");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { status: "resolved" });
  assert.equal(detail.issue.status, "resolved");
});

test("getEvent requests the event route and unwraps the envelope", async () => {
  const urls: string[] = [];
  const api = createApi(async (url) => {
    urls.push(url);
    return jsonResponse(200, {
      event: {
        event_id: "0b5f2f4e-1f3a-4c9e-9a1e-2b7c8d9e0f11",
        timestamp: "2026-08-11T10:00:00Z",
        received_at: "2026-08-11T10:00:01Z",
        level: "error",
        environment: "production",
        release: null,
        message: "Boom",
        request_id: null,
        payload: {},
        symbolicated_exception: null
      }
    });
  });

  const event = await api.getEvent("0b5f2f4e-1f3a-4c9e-9a1e-2b7c8d9e0f11");

  assert.equal(urls[0], "/api/v1/events/0b5f2f4e-1f3a-4c9e-9a1e-2b7c8d9e0f11");
  assert.equal(event.message, "Boom");
});

test("getEvent encodes the event id", async () => {
  const urls: string[] = [];
  const api = createApi(async (url) => {
    urls.push(url);
    return jsonResponse(200, { event: {} });
  });

  await api.getEvent("a b/c");

  // The id reaches the path segment, so an unencoded slash would address a
  // different route entirely rather than 404 honestly.
  assert.equal(urls[0], "/api/v1/events/a%20b%2Fc");
});

test("getTimeline requests the event timeline and returns spans", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      spans: [
        {
          span_id: "s1",
          parent_span_id: null,
          name: "root",
          start_time: "2026-07-15T12:00:00Z",
          duration_ms: 10,
          status: "ok",
          attributes: {},
        },
      ],
    }),
  );
  const api = createApi(fetch);

  const spans = await api.getTimeline("11111111-1111-1111-1111-111111111111");

  assert.equal(calls[0].url, "/api/v1/events/11111111-1111-1111-1111-111111111111/timeline");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal(spans.length, 1);
  assert.equal(spans[0].span_id, "s1");
  assert.equal(spans[0].parent_span_id, null);
  assert.equal(spans[0].name, "root");
});

test("listGroupingRules issues a same-origin GET scoped to the project slug", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { rules: [{ id: 1, project_id: 5, position: 1, matcher: "logger:billing", template: "billing" }] }),
  );
  const api = createApi(fetch);

  const rules = await api.listGroupingRules("web");

  assert.equal(calls[0].url, "/api/v1/projects/web/grouping-rules");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal(rules[0].matcher, "logger:billing");
  assert.equal(rules[0].project_id, 5);
  assert.equal(rules[0].position, 1);
});

test("createGroupingRule posts matcher and template", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(201, { id: 2, project_id: 5, position: 2, matcher: "logger:payments", template: "payments" }),
  );
  const api = createApi(fetch);

  const created = await api.createGroupingRule("web", { matcher: "logger:payments", template: "payments" });

  assert.equal(calls[0].url, "/api/v1/projects/web/grouping-rules");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { matcher: "logger:payments", template: "payments" });
  assert.equal(created.id, 2);
});

test("createGroupingRule surfaces the server's validation message on 400", async () => {
  const { fetch } = recordingFetch(jsonResponse(400, { error: "invalid grouping rule" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.createGroupingRule("web", { matcher: "not-a-valid-matcher", template: "x" }),
    (error: unknown) => error instanceof ApiError && error.status === 400 && error.message === "invalid grouping rule",
  );
});

test("updateGroupingRule patches an existing rule by id", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { id: 2, project_id: 5, position: 2, matcher: "logger:renamed", template: "renamed" }),
  );
  const api = createApi(fetch);

  const updated = await api.updateGroupingRule("web", 2, { matcher: "logger:renamed", template: "renamed" });

  assert.equal(calls[0].url, "/api/v1/projects/web/grouping-rules/2");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { matcher: "logger:renamed", template: "renamed" });
  assert.equal(updated.matcher, "logger:renamed");
});

test("moveGroupingRule posts a direction", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { id: 2, project_id: 5, position: 1, matcher: "logger:renamed", template: "renamed" }),
  );
  const api = createApi(fetch);

  const moved = await api.moveGroupingRule("web", 2, "up");

  assert.equal(calls[0].url, "/api/v1/projects/web/grouping-rules/2/move");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { direction: "up" });
  assert.equal(moved.position, 1);
});

test("deleteGroupingRule sends DELETE and expects no content", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  await api.deleteGroupingRule("web", 2);

  assert.equal(calls[0].url, "/api/v1/projects/web/grouping-rules/2");
  assert.equal(calls[0].init.method, "DELETE");
});

test("mergeIssue posts a target_id and returns the merged detail", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      issue: { id: 9, title: "Target", culprit: "", level: "error", status: "unresolved", assignee: null, times_seen: 2, first_seen: "2026-07-25T00:00:00Z", last_seen: "2026-07-25T00:00:00Z", counts_24h: [], counts_14d: [] },
      environments: [], releases: [], tags: [], regression: null,
    }),
  );
  const api = createApi(fetch);

  const merged = await api.mergeIssue(5, 9);

  assert.equal(calls[0].url, "/api/v1/issues/5/merge");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { target_id: 9 });
  assert.equal(merged.issue.id, 9);
});

test("mergeIssue surfaces the server's error when merging into itself", async () => {
  const { fetch } = recordingFetch(jsonResponse(400, { error: "cannot merge an issue into itself" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.mergeIssue(5, 5),
    (error: unknown) => error instanceof ApiError && error.status === 400 && error.message === "cannot merge an issue into itself",
  );
});

test("getIssue returns a normal detail and isMergedIssue reports false for it", async () => {
  const { fetch } = recordingFetch(
    jsonResponse(200, {
      issue: { id: 5, title: "Boom", culprit: "", level: "error", status: "unresolved", assignee: null, times_seen: 1, first_seen: "2026-07-16T00:00:00Z", last_seen: "2026-07-16T00:00:00Z", counts_24h: [], counts_14d: [] },
      environments: [], releases: [], tags: [], regression: null,
    }),
  );
  const api = createApi(fetch);

  const result = await api.getIssue(5);

  assert.equal(isMergedIssue(result), false);
  if (!isMergedIssue(result)) {
    assert.equal(result.issue.id, 5);
  }
});

test("getIssue returns a tombstone shape for a merged issue and isMergedIssue distinguishes it", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { merged_into_id: 9 }));
  const api = createApi(fetch);

  const result = await api.getIssue(5);

  assert.equal(calls[0].url, "/api/v1/issues/5");
  assert.ok(isMergedIssue(result));
  if (isMergedIssue(result)) {
    assert.equal(result.merged_into_id, 9);
  }
});

test("listUsers issues a same-origin GET and returns the managed users array", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      users: [
        { id: 1, email: "owner@example.com", name: "Owner", role: "owner", deactivated_at: null, created_at: "2026-08-04T00:00:00Z" },
      ],
    }),
  );
  const api = createApi(fetch);

  const users = await api.listUsers();

  assert.equal(calls[0].url, "/api/v1/users");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal(users[0].role, "owner");
});

test("listAssignableUsers issues a same-origin GET and returns the assignable users array", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, { users: [{ id: 2, name: "Member", email: "member@example.com" }] }),
  );
  const api = createApi(fetch);

  const users = await api.listAssignableUsers("web");

  assert.equal(calls[0].url, "/api/v1/users/assignable?project=web");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.deepEqual(users, [{ id: 2, name: "Member", email: "member@example.com" }]);
});

test("createUser posts JSON with same-origin credentials and returns the created user", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(201, {
      user: { id: 3, email: "new@example.com", name: "New", role: "member", deactivated_at: null, created_at: "2026-08-04T00:00:00Z" },
    }),
  );
  const api = createApi(fetch);

  const created = await api.createUser({ email: "new@example.com", name: "New", password: "correct horse battery", role: "member" });

  assert.equal(calls[0].url, "/api/v1/users");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), {
    email: "new@example.com",
    name: "New",
    password: "correct horse battery",
    role: "member",
  });
  assert.equal(created.email, "new@example.com");
});

test("patchUser sends only the supplied changes", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      user: { id: 3, email: "a@example.com", name: "A", role: "member", deactivated_at: null, created_at: "2026-08-04T00:00:00Z" },
    }),
  );
  const api = createApi(fetch);

  const updated = await api.patchUser(3, { deactivated: true });

  assert.equal(calls[0].url, "/api/v1/users/3");
  assert.equal(calls[0].init.method, "PATCH");
  // A PATCH that also sent name/role would overwrite fields the caller never
  // touched, which is the whole reason UserChanges is partial.
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { deactivated: true });
  assert.equal(updated.role, "member");
});

test("setUserPassword posts the password and expects no content", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  const result = await api.setUserPassword(3, "correct horse battery");

  assert.equal(calls[0].url, "/api/v1/users/3/password");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { password: "correct horse battery" });
  assert.equal(result, undefined);
});

test("changeOwnPassword posts the current and new password and expects no content", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  const result = await api.changeOwnPassword("the-real-one-12", "a-new-password-12");

  assert.equal(calls[0].url, "/api/v1/auth/password");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), {
    current_password: "the-real-one-12",
    new_password: "a-new-password-12"
  });
  assert.equal(result, undefined);
});

test("changeOwnPassword surfaces a wrong-current-password 403, distinct from a 401", async () => {
  const { fetch } = recordingFetch(jsonResponse(403, { error: "current password is incorrect" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.changeOwnPassword("wrong", "a-new-password-12"),
    (error: unknown) => error instanceof ApiError && error.status === 403,
  );
});

test("an owner-only route rejected for a member surfaces as a 403, distinct from a 401", async () => {
  const { fetch } = recordingFetch(jsonResponse(403, { error: "forbidden" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.listUsers(),
    (error: unknown) => error instanceof ApiError && error.status === 403,
  );
});

const TOKEN = {
  id: 3,
  project_id: 1,
  name: "gradle",
  token_prefix: "fla_ab12",
  created_at: "2026-09-28T08:00:00Z",
  last_used_at: null,
  revoked_at: null
};

test("listArtifactTokens reads the project's tokens from the artifact-tokens route", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { tokens: [TOKEN] }));
  const api = createApi(fetch);

  const tokens = await api.listArtifactTokens("web app");

  assert.equal(calls[0].url, "/api/v1/projects/web%20app/artifact-tokens");
  assert.equal(calls[0].init.method ?? "GET", "GET");
  assert.deepEqual(tokens, [TOKEN]);
});

test("createArtifactToken posts the name and returns the raw token beside the token record", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(201, { token: TOKEN, raw_token: "fla_secret" }));
  const api = createApi(fetch);

  const created = await api.createArtifactToken("web", "gradle");

  assert.equal(calls[0].url, "/api/v1/projects/web/artifact-tokens");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "gradle" });
  assert.equal(created.raw_token, "fla_secret");
  assert.equal(created.token.id, 3);
});

test("revokeArtifactToken posts to the revoke route and resolves on 204", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  await api.revokeArtifactToken("web", 3);

  assert.equal(calls[0].url, "/api/v1/projects/web/artifact-tokens/3/revoke");
  assert.equal(calls[0].init.method, "POST");
});

test("revokeArtifactToken surfaces the server's message when the token is not found", async () => {
  const { fetch } = recordingFetch(jsonResponse(404, { error: "artifact token not found" }));
  const api = createApi(fetch);

  await assert.rejects(api.revokeArtifactToken("web", 99), (error: unknown) => {
    assert.ok(error instanceof ApiError);
    assert.equal(error.status, 404);
    assert.equal(error.message, "artifact token not found");
    return true;
  });
});

test("getGlobalScrubbing issues a same-origin GET and returns the rules", async () => {
  const rules = [{ name: "n", pattern: "p", replacement: "" }];
  const { fetch, calls } = recordingFetch(jsonResponse(200, { rules }));
  const api = createApi(fetch);

  const scrubbing = await api.getGlobalScrubbing();

  assert.equal(calls[0].url, "/api/v1/settings/data-scrubbing");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.deepEqual(scrubbing, { rules });
});

test("updateGlobalScrubbing PUTs the rules as JSON", async () => {
  const rules = [{ name: "n", pattern: "p", replacement: "x" }];
  const { fetch, calls } = recordingFetch(jsonResponse(200, { rules }));
  const api = createApi(fetch);

  const scrubbing = await api.updateGlobalScrubbing({ rules });

  assert.equal(calls[0].url, "/api/v1/settings/data-scrubbing");
  assert.equal(calls[0].init.method, "PUT");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { rules });
  assert.deepEqual(scrubbing, { rules });
});

const notificationChannelInput = {
  name: "Ops",
  kind: "telegram" as const,
  target: "-1001234567890",
  secret: "123456789:token",
  enabled: true,
  notify_new: true,
  notify_regression: false,
  environments: ["production"],
  min_level: "error" as const,
};

test("listNotificationChannels issues a same-origin GET and unwraps channels", async () => {
  const { fetch, calls } = recordingFetch(
    jsonResponse(200, {
      channels: [{ ...notificationChannelInput, id: 3, secret: undefined, has_secret: true, last_success_at: null, last_error: "telegram: HTTP 403", last_error_at: "2026-10-15T09:00:00Z", created_at: "2026-10-15T08:00:00Z" }],
    }),
  );
  const api = createApi(fetch);

  const channels = await api.listNotificationChannels("web shop");

  assert.equal(calls[0].url, "/api/v1/projects/web%20shop/notification-channels");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal(channels[0].has_secret, true);
  assert.equal(channels[0].last_error, "telegram: HTTP 403");
});

test("createNotificationChannel posts the whole input", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(201, { ...notificationChannelInput, id: 3 }));
  const api = createApi(fetch);

  await api.createNotificationChannel("web", notificationChannelInput);

  assert.equal(calls[0].url, "/api/v1/projects/web/notification-channels");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), notificationChannelInput);
});

test("updateNotificationChannel PUTs by id and leaves an omitted secret out of the body", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(200, { ...notificationChannelInput, id: 3 }));
  const api = createApi(fetch);
  const { secret: _omitted, ...withoutSecret } = notificationChannelInput;

  await api.updateNotificationChannel("web", 3, withoutSecret);

  assert.equal(calls[0].url, "/api/v1/projects/web/notification-channels/3");
  assert.equal(calls[0].init.method, "PUT");
  assert.equal("secret" in JSON.parse(calls[0].init.body as string), false);
});

test("deleteNotificationChannel sends DELETE and expects no content", async () => {
  const { fetch, calls } = recordingFetch(new Response(null, { status: 204 }));
  const api = createApi(fetch);

  await api.deleteNotificationChannel("web", 3);

  assert.equal(calls[0].url, "/api/v1/projects/web/notification-channels/3");
  assert.equal(calls[0].init.method, "DELETE");
});

test("testNotificationChannel surfaces the destination's refusal from a 502", async () => {
  const { fetch, calls } = recordingFetch(jsonResponse(502, { error: "telegram: HTTP 400: Bad Request: chat not found" }));
  const api = createApi(fetch);

  await assert.rejects(
    () => api.testNotificationChannel("web", 3),
    (error: unknown) => error instanceof ApiError && error.status === 502 && error.message === "telegram: HTTP 400: Bad Request: chat not found",
  );
  assert.equal(calls[0].url, "/api/v1/projects/web/notification-channels/3/test");
  assert.equal(calls[0].init.method, "POST");
});
