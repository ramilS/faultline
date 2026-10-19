import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({ api: { listArtifacts: vi.fn() } }));

import { ApiError, type ArtifactPage } from "@/api";
import { api } from "@/apiClient";
import { ArtifactsPage } from "./ArtifactsPage";

const listArtifacts = vi.mocked(api.listArtifacts);

function renderPage() {
  return render(<MemoryRouter initialEntries={["/acme/settings/artifacts"]}><Routes>
    <Route path="/:slug/settings/artifacts" element={<ArtifactsPage />} />
  </Routes></MemoryRouter>);
}

const firstPage: ArtifactPage = {
  artifacts: [
    { type: "source_map", content_sha256: "aaaa", size_bytes: 1024, uploaded_at: "2026-10-18T12:00:00Z", last_used_at: null, references: [{ release: "web@1", generated_path: "/app.js" }, { debug_id: "debug-one" }] },
    { type: "proguard_mapping", mapping_uuid: "mapping-one", size_bytes: 2048, uploaded_at: "2026-10-18T11:00:00Z", last_used_at: "2026-10-18T13:00:00Z" }
  ],
  next_cursor: "next"
};

describe("ArtifactsPage", () => {
  beforeEach(() => {
    listArtifacts.mockReset();
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("renders mixed artifact metadata and appends the next page", async () => {
    listArtifacts.mockResolvedValueOnce(firstPage).mockResolvedValueOnce({
      artifacts: [{ type: "native_debug_file", content_sha256: "bbbb", size_bytes: 4096, uploaded_at: "2026-10-18T10:00:00Z", last_used_at: null, references: [{ debug_id: "native-one", cpu_name: "arm64" }] }],
      next_cursor: null
    });
    renderPage();

    expect(await screen.findByText("web@1 · /app.js")).toBeDefined();
    expect(screen.getByText("Debug ID debug-one")).toBeDefined();
    expect(screen.getByText("mapping-one")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("native-one · arm64")).toBeDefined();
    expect(screen.getByText("web@1 · /app.js")).toBeDefined();
    expect(listArtifacts).toHaveBeenLastCalledWith("acme", { cursor: "next" });
  });

  it("preserves existing rows and offers retry when load more fails", async () => {
    listArtifacts.mockResolvedValueOnce(firstPage).mockRejectedValueOnce(new Error("temporary failure")).mockResolvedValueOnce({ artifacts: [], next_cursor: null });
    renderPage();
    await screen.findByText("mapping-one");
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("temporary failure")).toBeDefined();
    expect(screen.getByText("mapping-one")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(listArtifacts).toHaveBeenCalledTimes(3));
  });

  it("shows contextual empty and inaccessible-project states", async () => {
    listArtifacts.mockResolvedValueOnce({ artifacts: [], next_cursor: null });
    const rendered = renderPage();
    expect(await screen.findByText("No artifacts yet")).toBeDefined();
    rendered.unmount();

    listArtifacts.mockRejectedValueOnce(new ApiError(404, "project not found"));
    renderPage();
    expect(await screen.findByText("Page not found")).toBeDefined();
  });

  it("reloads with the selected artifact type", async () => {
    listArtifacts.mockResolvedValue({ artifacts: [], next_cursor: null });
    renderPage();
    await screen.findByText("No artifacts yet");

    fireEvent.click(screen.getByRole("combobox", { name: "Artifact type" }));
    fireEvent.click(await screen.findByRole("option", { name: "Source maps" }));

    await waitFor(() => expect(listArtifacts).toHaveBeenLastCalledWith("acme", { type: "source_map" }));
    expect(await screen.findByText("No matching artifacts")).toBeDefined();
  });

  it("re-enables pagination when the filter changes during load more", async () => {
    let resolveSuperseded: ((page: ArtifactPage) => void) | undefined;
    const superseded = new Promise<ArtifactPage>((resolve) => { resolveSuperseded = resolve; });
    listArtifacts
      .mockResolvedValueOnce(firstPage)
      .mockReturnValueOnce(superseded)
      .mockResolvedValueOnce({ artifacts: firstPage.artifacts.slice(0, 1), next_cursor: "filtered-next" });
    renderPage();
    await screen.findByText("mapping-one");

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    fireEvent.click(screen.getByRole("combobox", { name: "Artifact type" }));
    fireEvent.click(await screen.findByRole("option", { name: "Source maps" }));

    await waitFor(() => expect(listArtifacts).toHaveBeenLastCalledWith("acme", { type: "source_map" }));
    expect((await screen.findByRole("button", { name: "Load more" }) as HTMLButtonElement).disabled).toBe(false);
    resolveSuperseded?.({ artifacts: [], next_cursor: null });
  });
});
