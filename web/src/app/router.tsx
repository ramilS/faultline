import { Navigate, Route, Routes } from "react-router-dom";

import { AppShell } from "@/app/AppShell";
import { NotFoundPage } from "@/app/NotFoundPage";
import { RequireOwner } from "@/app/RequireOwner";
import { RequireSession } from "@/app/RequireSession";
import { ScrollToTop } from "@/app/ScrollToTop";
import { AccountPage } from "@/features/account/AccountPage";
import { LoginPage } from "@/features/auth/LoginPage";
import { IssueDetailPage } from "@/features/issue-detail/IssueDetailPage";
import { GroupsPage } from "@/features/groups/GroupsPage";
import { IssueEventsPage } from "@/features/issue-detail/IssueEventsPage";
import { IssueStreamPage } from "@/features/issues/IssueStreamPage";
import { ProjectsPage } from "@/features/projects/ProjectsPage";
import { ProjectsProvider } from "@/features/projects/ProjectsProvider";
import { ArtifactTokensPage } from "@/features/settings/ArtifactTokensPage";
import { DataScrubbingPage } from "@/features/settings/DataScrubbingPage";
import { GlobalDataScrubbingPage } from "@/features/settings/GlobalDataScrubbingPage";
import { GroupingRulesPage } from "@/features/settings/GroupingRulesPage";
import { KeysPage } from "@/features/settings/KeysPage";
import { ProjectGeneralPage } from "@/features/settings/ProjectGeneralPage";
import { UsersPage } from "@/features/users/UsersPage";

/**
 * `/login` is the only unguarded route. Everything else renders inside
 * `RequireSession` and the shell.
 *
 * Filters live in the query string rather than component state, so a filtered
 * stream and an individual issue are both shareable URLs.
 */
export function AppRouter() {
  return (
    <>
      <ScrollToTop />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <RequireSession>
              <ProjectsProvider>
                <AppShell />
              </ProjectsProvider>
            </RequireSession>
          }
        >
          <Route path="/" element={<Navigate to="/projects" replace />} />
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/settings" element={<Navigate to="account" replace />} />
          <Route path="/settings/account" element={<AccountPage />} />
          <Route
            path="/settings/users"
            element={
              <RequireOwner>
                <UsersPage />
              </RequireOwner>
            }
          />
          <Route
            path="/settings/groups"
            element={
              <RequireOwner subject="groups">
                <GroupsPage />
              </RequireOwner>
            }
          />
          <Route
            path="/settings/data-scrubbing"
            element={
              <RequireOwner subject="data scrubbing">
                <GlobalDataScrubbingPage />
              </RequireOwner>
            }
          />
          {/* A project's own URL and its settings folder have no screen of their
              own; landing on them from a trimmed link should reach the nearest
              real one rather than a not-found page. */}
          <Route path="/:slug" element={<Navigate to="issues" replace />} />
          <Route path="/:slug/settings" element={<Navigate to="keys" replace />} />
          <Route path="/:slug/issues" element={<IssueStreamPage />} />
          <Route path="/:slug/issues/:issueId" element={<IssueDetailPage />} />
          <Route path="/:slug/issues/:issueId/events" element={<IssueEventsPage />} />
          <Route path="/:slug/settings/general" element={<ProjectGeneralPage />} />
          <Route path="/:slug/settings/keys" element={<KeysPage />} />
          <Route path="/:slug/settings/artifact-tokens" element={<ArtifactTokensPage />} />
          <Route path="/:slug/settings/grouping" element={<GroupingRulesPage />} />
          <Route path="/:slug/settings/data-scrubbing" element={<DataScrubbingPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </>
  );
}
