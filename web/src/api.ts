export interface Project {
  icon_emoji?: string;
  icon_image?: string;
  id: number;
  slug: string;
  name: string;
  platform: string;
  created_at: string;
}

export interface Key {
  id: number;
  project_id: number;
  public_key: string;
  rate_limit_rps: number;
  rate_limit_burst: number;
  revoked_at: string | null;
}

// ArtifactToken is the per-project credential sentry-cli and the Gradle plugin
// present when uploading source maps and ProGuard mappings. Listing carries
// only a prefix; the raw value exists solely in CreatedArtifactToken.
export interface ArtifactToken {
  id: number;
  project_id: number;
  name: string;
  token_prefix: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

// CreatedArtifactToken is the only shape that carries a raw artifact token,
// returned once at creation and never retrievable again.
export interface CreatedArtifactToken {
  token: ArtifactToken;
  raw_token: string;
}

export interface CreateProjectInput {
  icon_emoji?: string;
  icon_image?: string;
  slug: string;
  name: string;
  platform: string;
}

/** The slug is not editable: it is the project's address in URLs and upload paths. */
export interface UpdateProjectInput {
  icon_emoji?: string;
  icon_image?: string;
  name: string;
  platform: string;
}

export interface CreateKeyInput {
  rate_limit_rps: number;
  rate_limit_burst: number;
}

// CreatedKey is the only shape that carries raw key material, returned once at
// creation. The persisted Key type deliberately has no raw field, so listings
// can never expose it.
export interface CreatedKey {
  key: Key;
  raw_key: string;
}

export type IssueStatus = "unresolved" | "resolved" | "ignored";
export type IssueLevel = "fatal" | "error" | "warning" | "info" | "debug";
export type IssueSort = "last_seen" | "times_seen" | "first_seen" | "users_seen";
export type StatsPeriod = "24h" | "14d" | "90d";
// The issue list's trailing window, distinct from StatsPeriod above: that one
// names the fixed set the per-issue sparkline endpoint accepts, this one names
// the wider set (adds 7d/30d) the list endpoint's statsPeriod parameter accepts.
export type WindowPeriod = "24h" | "7d" | "14d" | "30d" | "90d";

export interface Assignee {
  id: number;
  name: string;
  email: string;
}

export interface IssueSummary {
  id: number;
  project_slug: string;
  fingerprint: string;
  title: string;
  culprit: string;
  level: string;
  status: IssueStatus;
  assignee: Assignee | null;
  times_seen: number;
  // Past ingest.MaxUsersPerIssue (10 000) this is a floor, not an exact
  // count: the server stops recording new identities once the cap is hit,
  // so a client rendering this value should treat it as "at least" past
  // that point. See internal/api/issues.go's issueResponse.UsersSeen.
  users_seen: number;
  first_seen: string;
  last_seen: string;
  // A windowed list request (any statsPeriod or absolute range) populates
  // counts/period_count and omits the legacy pair; a windowless one is the
  // opposite. Exactly one of the two shapes is present on any given summary.
  counts?: number[];
  period_count?: number;
  counts_24h?: number[];
  counts_14d?: number[];
}

export interface TagValue {
  value: string;
  times_seen: number;
  first_seen: string;
  last_seen: string;
}

export interface TagFacet {
  key: string;
  values: TagValue[];
}

// TagKeyFacet is a project's tag vocabulary, one entry per key — the filter
// bar's key picker. TagValue (above) is reused for that key's values, since
// GET /tags/{key}/values returns the identical shape as an issue's own facets.
export interface TagKeyFacet {
  key: string;
  value_count: number;
  times_seen: number;
}

export interface IssueDetail {
  issue: IssueSummary;
  environments: string[];
  releases: string[];
  tags: TagFacet[];
}

// AffectedUser is one row of the issue's users facet. `key` names which of
// the envelope's four identity fields was counted for this row — precedence
// is id > email > username > ip (internal/ingest/sentry_user.go) — so
// rendering it alongside `value` is what makes an aggregate count honest
// about what was actually counted.
export interface AffectedUser {
  key: "id" | "email" | "username" | "ip";
  value: string;
  times_seen: number;
  last_seen: string;
}

// IssueUsersFacet mirrors internal/api/users_facet.go's issueUsersResponse.
// `total` is the issue's distinct-identity count (a floor once `saturated`),
// read independently of `users` — a concurrent ingest flush can make the
// two individually correct but not arithmetically consistent, so a caller
// must never subtract one from the other. `saturated` is derived
// server-side from `total` against ingest.MaxUsersPerIssue; the frontend
// never hardcodes that cap itself.
export interface IssueUsersFacet {
  users: AffectedUser[];
  total: number;
  saturated: boolean;
}

export interface EventSummary {
  event_id: string;
  timestamp: string;
  received_at: string;
  level: string;
  environment: string;
  release: string | null;
  message: string;
}

/**
 * Which end of an issue's event stream to page from. The server defaults to
 * `newest`; `oldest` pages forward from the first occurrence.
 */
export type EventOrder = "newest" | "oldest";

export interface ListIssueEventsOptions {
  /** Only valid under the `order` that produced it — the two are not interchangeable. */
  cursor?: string;
  order?: EventOrder;
  /** Exact match against the event's recorded environment. Omitted (or "") means every environment. */
  environment?: string;
  /**
   * Absolute RFC3339 bounds only — no `statsPeriod` field. The event pager
   * walks a cursor across several requests, and the server resolves a
   * trailing window against wall-clock `now()` on every request (see
   * `internal/api/issues.go`'s `parseEventWindow`), so sending a relative
   * period here would let the window slide forward mid-walk and drop events
   * the walk had not yet reached. `eventWindow.ts` resolves the picker's
   * relative choice to a frozen `start`/`end` pair once, at pick time.
   */
  start?: string;
  end?: string;
}

export interface SymbolicatedPosition {
  function: string;
  filename: string;
  line: number;
  column: number;
  in_app: boolean;
}

export interface SourceContextLine {
  line: number;
  text: string;
  focus: boolean;
}

export interface SymbolicatedFrame {
  generated: SymbolicatedPosition;
  original: SymbolicatedPosition | null;
  context: SourceContextLine[];
}

export interface SymbolicatedException {
  type: string;
  value: string;
  frames: SymbolicatedFrame[];
  cause: SymbolicatedException | null;
}

export interface EventDetail extends EventSummary {
  request_id: string | null;
  payload: unknown;
  symbolicated_exception: SymbolicatedException | null;
}

export interface Span {
  span_id: string;
  parent_span_id: string | null;
  name: string;
  start_time: string;
  duration_ms: number;
  status: string;
  attributes: unknown;
}

export interface Bucket {
  bucket: string;
  count: number;
}

export interface IssueFilters {
  statuses?: IssueStatus[];
  levels?: IssueLevel[];
  // "me", "none", or an email — see internal/api/issues.go's assigned handling.
  assigned?: string;
  environments?: string[];
  releases?: string[];
  // "key:value" pairs; negation and wildcard escaping already applied.
  tags?: string[];
  statsPeriod?: WindowPeriod;
  start?: string;
  end?: string;
  query?: string;
  sort?: IssueSort;
  limit?: number;
  cursor?: string;
}

export interface IssuePage {
  issues: IssueSummary[];
  next_cursor: string | null;
}

export interface IssueChanges {
  status?: IssueStatus;
  assignee_id?: number | null;
}

export interface ScrubRule {
  name: string;
  pattern: string;
  replacement: string;
}

export interface DataScrubbing {
  enabled: boolean;
  extra_fields: string[];
  safe_fields: string[];
  rules: ScrubRule[];
}

export interface GlobalScrubbing {
  rules: ScrubRule[];
}

export type NotificationChannelKind = "telegram" | "webhook";

export type NotificationLevel = "debug" | "info" | "warning" | "error" | "fatal";

/**
 * A destination for a project's new-issue and regression alerts. The secret
 * (bot token or webhook signing key) is write-only: the server only ever says
 * whether one is set.
 */
export interface NotificationChannel {
  id: number;
  name: string;
  kind: NotificationChannelKind;
  /** A Telegram chat id or @username, or a webhook URL. */
  target: string;
  has_secret: boolean;
  enabled: boolean;
  notify_new: boolean;
  notify_regression: boolean;
  /** Empty means every environment. */
  environments: string[];
  min_level: NotificationLevel;
  last_success_at: string | null;
  last_error: string | null;
  last_error_at: string | null;
  created_at: string;
}

/**
 * Create or full update. Omitting `secret` on update keeps the stored one;
 * `kind` cannot change after creation.
 */
export interface NotificationChannelInput {
  name: string;
  kind: NotificationChannelKind;
  target: string;
  secret?: string;
  enabled: boolean;
  notify_new: boolean;
  notify_regression: boolean;
  environments: string[];
  min_level: NotificationLevel;
}

export interface GroupingRule {
  id: number;
  project_id: number;
  position: number;
  matcher: string;
  template: string;
}

export interface CreateGroupingRuleInput {
  matcher: string;
  template: string;
}

export type UpdateGroupingRuleInput = CreateGroupingRuleInput;

export type GroupingRuleMoveDirection = "up" | "down";

// MergedIssue is the shape GET /api/v1/issues/{id} returns for a tombstoned
// (merged-away) issue instead of the normal IssueDetail body.
export interface MergedIssue {
  merged_into_id: number;
}

export type IssueDetailResult = IssueDetail | MergedIssue;

export function isMergedIssue(result: IssueDetailResult): result is MergedIssue {
  return "merged_into_id" in result;
}

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export class ApiError extends Error {
  readonly status: number;
  /**
   * Seconds from the `Retry-After` header, when the server sent one. The login
   * limiter (`internal/auth/limiter.go`) sets it on 429 so the form can say how
   * long to wait instead of reporting a bare failure.
   */
  readonly retryAfterSeconds: number | null;

  constructor(status: number, message: string, retryAfterSeconds: number | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

export type UserRole = "owner" | "member";

export interface AuthenticatedUser {
  id: number;
  name: string;
  email: string;
  role: UserRole;
}

// ManagedUser is the owner-only administrative view: every field the
// /users list and mutation endpoints expose.
export interface ManagedUser {
  id: number;
  email: string;
  name: string;
  role: UserRole;
  deactivated_at: string | null;
  created_at: string;
}

// AssignableUser mirrors the server's deliberately narrow
// /users/assignable shape. It must never gain `role` or `deactivated_at`:
// those are administrative facts a member must not learn about every user,
// and the server pins the exact key set with its own test.
export interface AssignableUser {
  id: number;
  name: string;
  email: string;
}

export interface CreateUserInput {
  email: string;
  name: string;
  password: string;
  role: UserRole;
}

export interface UserChanges {
  name?: string;
  role?: UserRole;
  deactivated?: boolean;
}

export interface Credentials {
  email: string;
  password: string;
}

// The unauthenticated shape GET /api/v1/auth/providers returns. There is no
// issuer or domain to show — just whether the deployment has SSO configured
// at all, which is what the login page uses to decide whether to render the
// button.
export interface AuthProviders {
  sso: { enabled: boolean };
}

// Standalone rather than a FaultlineApi method: it is called before sign-in,
// with no session-aware fetch to route through, so it goes straight to the
// global fetch like the rest of this module's request() calls do.
export async function fetchAuthProviders(): Promise<AuthProviders> {
  return request<AuthProviders>(fetch, "/api/v1/auth/providers", { method: "GET" });
}

// Group is the /api/v1/groups list shape: metadata plus counts. An empty
// group (member_count and project_count both 0) is a normal state, not an
// error — there is deliberately no grandfathering group.
export interface Group {
  id: number;
  name: string;
  member_count: number;
  project_count: number;
  created_at: string;
}

// GroupDetail is what GET /api/v1/groups/{id} adds to Group: the exact
// membership and project grants, by id.
export interface GroupDetail extends Group {
  user_ids: number[];
  project_ids: number[];
}

export interface CreateGroupInput {
  name: string;
}

export interface GroupChanges {
  name: string;
}

export interface FaultlineApi {
  login(credentials: Credentials): Promise<AuthenticatedUser>;
  logout(): Promise<void>;
  me(): Promise<AuthenticatedUser>;
  listProjects(): Promise<Project[]>;
  createProject(input: CreateProjectInput): Promise<Project>;
  updateProject(slug: string, input: UpdateProjectInput): Promise<Project>;
  listKeys(slug: string): Promise<Key[]>;
  createKey(slug: string, input: CreateKeyInput): Promise<CreatedKey>;
  revokeKey(slug: string, keyId: number): Promise<void>;
  listArtifactTokens(slug: string): Promise<ArtifactToken[]>;
  createArtifactToken(slug: string, name: string): Promise<CreatedArtifactToken>;
  revokeArtifactToken(slug: string, tokenId: number): Promise<void>;
  /** A null slug lists every project the caller can read. */
  listIssues(slug: string | null, filters?: IssueFilters): Promise<IssuePage>;
  listTagKeys(slug: string): Promise<TagKeyFacet[]>;
  listTagValues(slug: string, key: string, query?: string, signal?: AbortSignal): Promise<TagValue[]>;
  getIssue(id: number): Promise<IssueDetailResult>;
  patchIssue(id: number, changes: IssueChanges): Promise<IssueDetail>;
  listIssueEvents(id: number, options?: ListIssueEventsOptions): Promise<{ events: EventSummary[]; next_cursor: string | null }>;
  getEvent(eventId: string): Promise<EventDetail>;
  getIssueStats(id: number, period: StatsPeriod): Promise<Bucket[]>;
  listIssueUsers(id: number): Promise<IssueUsersFacet>;
  getTimeline(eventId: string): Promise<Span[]>;
  getDataScrubbing(slug: string): Promise<DataScrubbing>;
  updateDataScrubbing(slug: string, input: DataScrubbing): Promise<DataScrubbing>;
  getGlobalScrubbing(): Promise<GlobalScrubbing>;
  updateGlobalScrubbing(input: GlobalScrubbing): Promise<GlobalScrubbing>;
  listNotificationChannels(slug: string): Promise<NotificationChannel[]>;
  createNotificationChannel(slug: string, input: NotificationChannelInput): Promise<NotificationChannel>;
  updateNotificationChannel(slug: string, channelId: number, input: NotificationChannelInput): Promise<NotificationChannel>;
  deleteNotificationChannel(slug: string, channelId: number): Promise<void>;
  /** Sends a sample message now; rejects with the destination's own error when it refuses. */
  testNotificationChannel(slug: string, channelId: number): Promise<void>;
  listGroupingRules(slug: string): Promise<GroupingRule[]>;
  createGroupingRule(slug: string, input: CreateGroupingRuleInput): Promise<GroupingRule>;
  updateGroupingRule(slug: string, ruleId: number, input: UpdateGroupingRuleInput): Promise<GroupingRule>;
  moveGroupingRule(slug: string, ruleId: number, direction: GroupingRuleMoveDirection): Promise<GroupingRule>;
  deleteGroupingRule(slug: string, ruleId: number): Promise<void>;
  mergeIssue(id: number, targetId: number): Promise<IssueDetail>;
  listUsers(): Promise<ManagedUser[]>;
  /**
   * `GET /api/v1/users/assignable?project=`, the list behind the issue's
   * assignee menu. `?project=` is required server-side, and assignability is
   * enforced there too, not just by what the menu offers.
   */
  listAssignableUsers(projectSlug: string): Promise<AssignableUser[]>;
  createUser(input: CreateUserInput): Promise<ManagedUser>;
  patchUser(id: number, changes: UserChanges): Promise<ManagedUser>;
  setUserPassword(id: number, password: string): Promise<void>;
  changeOwnPassword(currentPassword: string, newPassword: string): Promise<void>;
  listGroups(): Promise<Group[]>;
  getGroup(id: number): Promise<GroupDetail>;
  createGroup(input: CreateGroupInput): Promise<Group>;
  renameGroup(id: number, changes: GroupChanges): Promise<Group>;
  deleteGroup(id: number): Promise<void>;
  replaceGroupMembers(id: number, userIds: number[]): Promise<number[]>;
  replaceGroupProjects(id: number, projectIds: number[]): Promise<number[]>;
}

export function createApi(fetchImpl: FetchLike = fetch): FaultlineApi {
  return {
    login: async (credentials) => {
      const body = await request<{ user: AuthenticatedUser }>(fetchImpl, "/api/v1/auth/login", {
        method: "POST",
        json: credentials
      });
      return body.user;
    },
    logout: async () => {
      await request<void>(fetchImpl, "/api/v1/auth/logout", { method: "POST", expectNoContent: true });
    },
    me: async () => {
      const body = await request<{ user: AuthenticatedUser }>(fetchImpl, "/api/v1/auth/me", { method: "GET" });
      return body.user;
    },
    listProjects: async () => {
      const body = await request<{ projects: Project[] }>(fetchImpl, "/api/v1/projects", { method: "GET" });
      return body.projects;
    },
    createProject: async (input) => {
      const body = await request<{ project: Project }>(fetchImpl, "/api/v1/projects", { method: "POST", json: input });
      return body.project;
    },
    updateProject: async (slug, input) => {
      const body = await request<{ project: Project }>(fetchImpl, `/api/v1/projects/${encodeURIComponent(slug)}`, {
        method: "PATCH",
        json: input
      });
      return body.project;
    },
    listKeys: async (slug) => {
      const body = await request<{ keys: Key[] }>(fetchImpl, keysPath(slug), { method: "GET" });
      return body.keys;
    },
    createKey: async (slug, input) => {
      return request<CreatedKey>(fetchImpl, keysPath(slug), { method: "POST", json: input });
    },
    revokeKey: async (slug, keyId) => {
      await request<void>(fetchImpl, `${keysPath(slug)}/${keyId}/revoke`, { method: "POST", expectNoContent: true });
    },
    listArtifactTokens: async (slug) => {
      const body = await request<{ tokens: ArtifactToken[] }>(fetchImpl, artifactTokensPath(slug), { method: "GET" });
      return body.tokens;
    },
    createArtifactToken: async (slug, name) => {
      return request<CreatedArtifactToken>(fetchImpl, artifactTokensPath(slug), { method: "POST", json: { name } });
    },
    revokeArtifactToken: async (slug, tokenId) => {
      await request<void>(fetchImpl, `${artifactTokensPath(slug)}/${tokenId}/revoke`, {
        method: "POST",
        expectNoContent: true
      });
    },
    listIssues: async (slug, filters = {}) => {
      const query = new URLSearchParams();
      for (const status of filters.statuses ?? []) query.append("status", status);
      for (const level of filters.levels ?? []) query.append("level", level);
      if (filters.assigned) query.set("assigned", filters.assigned);
      for (const environment of filters.environments ?? []) query.append("environment", environment);
      for (const release of filters.releases ?? []) query.append("release", release);
      for (const tag of filters.tags ?? []) query.append("tag", tag);
      if (filters.statsPeriod) query.set("statsPeriod", filters.statsPeriod);
      if (filters.start) query.set("start", filters.start);
      if (filters.end) query.set("end", filters.end);
      if (filters.query) query.set("query", filters.query);
      if (filters.sort) query.set("sort", filters.sort);
      if (filters.limit) query.set("limit", String(filters.limit));
      if (filters.cursor) query.set("cursor", filters.cursor);
      const suffix = query.toString() ? `?${query.toString()}` : "";
      const path = slug === null ? "/api/v1/issues" : `/api/v1/projects/${encodeURIComponent(slug)}/issues`;
      return request<IssuePage>(fetchImpl, `${path}${suffix}`, { method: "GET" });
    },
    listTagKeys: async (slug) => {
      const body = await request<{ tags: TagKeyFacet[] }>(fetchImpl, tagsPath(slug), { method: "GET" });
      return body.tags;
    },
    listTagValues: async (slug, key, query, signal) => {
      const suffix = query ? `?query=${encodeURIComponent(query)}` : "";
      const body = await request<{ values: TagValue[] }>(
        fetchImpl,
        `${tagsPath(slug)}/${encodeURIComponent(key)}/values${suffix}`,
        { method: "GET", signal }
      );
      return body.values;
    },
    getIssue: async (id) => request<IssueDetailResult>(fetchImpl, `/api/v1/issues/${id}`, { method: "GET" }),
    patchIssue: async (id, changes) => request<IssueDetail>(fetchImpl, `/api/v1/issues/${id}`, { method: "PATCH", json: changes }),
    listIssueEvents: async (id, options) => {
      const query = new URLSearchParams();
      if (options?.cursor) {
        query.set("cursor", options.cursor);
      }
      // Omitted rather than sent as "newest": the server defaults to it, and a
      // cursor only means anything under the order that produced it.
      if (options?.order === "oldest") {
        query.set("order", "oldest");
      }
      if (options?.environment) {
        query.set("environment", options.environment);
      }
      if (options?.start) {
        query.set("start", options.start);
      }
      if (options?.end) {
        query.set("end", options.end);
      }
      const search = query.toString();
      const suffix = search === "" ? "" : `?${search}`;
      return request<{ events: EventSummary[]; next_cursor: string | null }>(fetchImpl, `/api/v1/issues/${id}/events${suffix}`, { method: "GET" });
    },
    getEvent: async (eventId) => {
      const body = await request<{ event: EventDetail }>(
        fetchImpl,
        `/api/v1/events/${encodeURIComponent(eventId)}`,
        { method: "GET" }
      );
      return body.event;
    },
    getIssueStats: async (id, period) => {
      const body = await request<{ period: string; buckets: Bucket[] }>(fetchImpl, `/api/v1/issues/${id}/stats?period=${period}`, { method: "GET" });
      return body.buckets;
    },
    // No `limit` param: mirrors listIssueEvents above — the server defaults
    // it (parseFacetLimit in internal/api/users_facet.go), and this facet
    // has no UI control that would need to change it.
    listIssueUsers: async (id) => {
      return request<IssueUsersFacet>(fetchImpl, `/api/v1/issues/${id}/users`, { method: "GET" });
    },
    getTimeline: async (eventId) => {
      const body = await request<{ spans: Span[] }>(
        fetchImpl,
        `/api/v1/events/${encodeURIComponent(eventId)}/timeline`,
        { method: "GET" },
      );
      return body.spans;
    },
    getDataScrubbing: async (slug) => {
      return request<DataScrubbing>(fetchImpl, dataScrubbingPath(slug), { method: "GET" });
    },
    updateDataScrubbing: async (slug, input) => {
      return request<DataScrubbing>(fetchImpl, dataScrubbingPath(slug), { method: "PUT", json: input });
    },
    getGlobalScrubbing: async () => {
      return request<GlobalScrubbing>(fetchImpl, "/api/v1/settings/data-scrubbing", { method: "GET" });
    },
    updateGlobalScrubbing: async (input) => {
      return request<GlobalScrubbing>(fetchImpl, "/api/v1/settings/data-scrubbing", { method: "PUT", json: input });
    },
    listGroupingRules: async (slug) => {
      const body = await request<{ rules: GroupingRule[] }>(fetchImpl, groupingRulesPath(slug), { method: "GET" });
      return body.rules;
    },
    createGroupingRule: async (slug, input) => {
      return request<GroupingRule>(fetchImpl, groupingRulesPath(slug), { method: "POST", json: input });
    },
    updateGroupingRule: async (slug, ruleId, input) => {
      return request<GroupingRule>(fetchImpl, `${groupingRulesPath(slug)}/${ruleId}`, { method: "PATCH", json: input });
    },
    moveGroupingRule: async (slug, ruleId, direction) => {
      return request<GroupingRule>(fetchImpl, `${groupingRulesPath(slug)}/${ruleId}/move`, { method: "POST", json: { direction } });
    },
    deleteGroupingRule: async (slug, ruleId) => {
      await request<void>(fetchImpl, `${groupingRulesPath(slug)}/${ruleId}`, { method: "DELETE", expectNoContent: true });
    },
    listNotificationChannels: async (slug) => {
      const body = await request<{ channels: NotificationChannel[] }>(fetchImpl, notificationChannelsPath(slug), { method: "GET" });
      return body.channels;
    },
    createNotificationChannel: async (slug, input) => {
      return request<NotificationChannel>(fetchImpl, notificationChannelsPath(slug), { method: "POST", json: input });
    },
    updateNotificationChannel: async (slug, channelId, input) => {
      return request<NotificationChannel>(fetchImpl, `${notificationChannelsPath(slug)}/${channelId}`, { method: "PUT", json: input });
    },
    deleteNotificationChannel: async (slug, channelId) => {
      await request<void>(fetchImpl, `${notificationChannelsPath(slug)}/${channelId}`, { method: "DELETE", expectNoContent: true });
    },
    testNotificationChannel: async (slug, channelId) => {
      await request<void>(fetchImpl, `${notificationChannelsPath(slug)}/${channelId}/test`, { method: "POST", expectNoContent: true });
    },
    mergeIssue: async (id, targetId) => {
      return request<IssueDetail>(fetchImpl, `/api/v1/issues/${id}/merge`, { method: "POST", json: { target_id: targetId } });
    },
    listUsers: async () => {
      const body = await request<{ users: ManagedUser[] }>(fetchImpl, usersPath, { method: "GET" });
      return body.users;
    },
    listAssignableUsers: async (projectSlug) => {
      const body = await request<{ users: AssignableUser[] }>(
        fetchImpl,
        `${usersPath}/assignable?project=${encodeURIComponent(projectSlug)}`,
        { method: "GET" },
      );
      return body.users;
    },
    createUser: async (input) => {
      const body = await request<{ user: ManagedUser }>(fetchImpl, usersPath, { method: "POST", json: input });
      return body.user;
    },
    patchUser: async (id, changes) => {
      const body = await request<{ user: ManagedUser }>(fetchImpl, `${usersPath}/${id}`, { method: "PATCH", json: changes });
      return body.user;
    },
    setUserPassword: async (id, password) => {
      await request<void>(fetchImpl, `${usersPath}/${id}/password`, { method: "POST", json: { password }, expectNoContent: true });
    },
    changeOwnPassword: async (currentPassword, newPassword) => {
      await request<void>(fetchImpl, "/api/v1/auth/password", {
        method: "POST",
        json: { current_password: currentPassword, new_password: newPassword },
        expectNoContent: true
      });
    },
    listGroups: async () => {
      const body = await request<{ groups: Group[] }>(fetchImpl, groupsPath, { method: "GET" });
      return body.groups;
    },
    getGroup: async (id) => request<GroupDetail>(fetchImpl, `${groupsPath}/${id}`, { method: "GET" }),
    createGroup: async (input) => {
      return request<Group>(fetchImpl, groupsPath, { method: "POST", json: input });
    },
    renameGroup: async (id, changes) => {
      return request<Group>(fetchImpl, `${groupsPath}/${id}`, { method: "PATCH", json: changes });
    },
    deleteGroup: async (id) => {
      await request<void>(fetchImpl, `${groupsPath}/${id}`, { method: "DELETE", expectNoContent: true });
    },
    // The response is the applied set, not the request body: the server
    // deduplicates and normalizes nil to [], so the caller must render what
    // comes back rather than assume the request body was stored verbatim.
    replaceGroupMembers: async (id, userIds) => {
      const body = await request<{ user_ids: number[] }>(fetchImpl, `${groupsPath}/${id}/members`, {
        method: "PUT",
        json: { user_ids: userIds }
      });
      return body.user_ids;
    },
    replaceGroupProjects: async (id, projectIds) => {
      const body = await request<{ project_ids: number[] }>(fetchImpl, `${groupsPath}/${id}/projects`, {
        method: "PUT",
        json: { project_ids: projectIds }
      });
      return body.project_ids;
    },
  };
}

interface RequestOptions {
  method: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  json?: unknown;
  expectNoContent?: boolean;
  // Lets a caller cancel an in-flight GET — autocomplete uses it so a stale
  // suggestion request can't resolve after a newer keystroke superseded it.
  signal?: AbortSignal;
}

async function request<T>(fetchImpl: FetchLike, url: string, options: RequestOptions): Promise<T> {
  const init: RequestInit = { method: options.method, credentials: "same-origin", signal: options.signal };
  if (options.json !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(options.json);
  }

  const response = await fetchImpl(url, init);
  if (!response.ok) {
    throw new ApiError(response.status, await errorMessage(response), retryAfterSeconds(response));
  }
  if (options.expectNoContent) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

function retryAfterSeconds(response: Response): number | null {
  const header = response.headers.get("Retry-After");
  if (header === null) {
    return null;
  }
  const seconds = Number.parseInt(header, 10);
  return Number.isFinite(seconds) && seconds >= 0 ? seconds : null;
}

async function errorMessage(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown };
    if (typeof body.error === "string" && body.error !== "") {
      return body.error;
    }
  } catch {
    // Fall through to a generic status message when the body is not JSON.
  }
  return `request failed with status ${response.status}`;
}

const usersPath = "/api/v1/users";
const groupsPath = "/api/v1/groups";

function artifactTokensPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/artifact-tokens`;
}

function keysPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/keys`;
}

function dataScrubbingPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/data-scrubbing`;
}

function notificationChannelsPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/notification-channels`;
}

function groupingRulesPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/grouping-rules`;
}

function tagsPath(slug: string): string {
  return `/api/v1/projects/${encodeURIComponent(slug)}/tags`;
}
