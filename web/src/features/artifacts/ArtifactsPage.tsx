import { Boxes } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Navigate, useParams } from "react-router-dom";

import { ApiError, type ArtifactPage, type ArtifactType, type ProjectArtifact } from "@/api";
import { api } from "@/apiClient";
import { NotFoundPage } from "@/app/NotFoundPage";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { RelativeTime } from "@/components/RelativeTime";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

type Filter = "all" | ArtifactType;
type LoadState =
  | { status: "loading" }
  | { status: "failed"; message: string; notFound: boolean }
  | { status: "ready"; page: ArtifactPage };

export function ArtifactsPage() {
  const { slug } = useParams<{ slug: string }>();
  return slug === undefined ? <Navigate to="/projects" replace /> : <ArtifactsScreen slug={slug} />;
}

function ArtifactsScreen({ slug }: { slug: string }) {
  useDocumentTitle(`Artifacts · ${slug}`);
  const [filter, setFilter] = useState<Filter>("all");
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const generation = useRef(0);

  const loadFirst = useCallback(async () => {
    const requestGeneration = ++generation.current;
    setState({ status: "loading" });
    setLoadingMore(false);
    setLoadMoreError(null);
    try {
      const page = await api.listArtifacts(slug, filter === "all" ? {} : { type: filter });
      if (generation.current !== requestGeneration) return;
      setState({ status: "ready", page });
    } catch (error) {
      if (generation.current !== requestGeneration) return;
      setState({ status: "failed", message: error instanceof Error ? error.message : "Unable to load artifacts.", notFound: error instanceof ApiError && error.status === 404 });
    }
  }, [filter, slug]);

  useEffect(() => { void loadFirst(); }, [loadFirst]);

  const loadMore = async () => {
    if (state.status !== "ready" || state.page.next_cursor === null || loadingMore) return;
    const requestGeneration = generation.current;
    setLoadingMore(true);
    setLoadMoreError(null);
    try {
      const next = await api.listArtifacts(slug, { ...(filter === "all" ? {} : { type: filter }), cursor: state.page.next_cursor });
      if (generation.current !== requestGeneration) return;
      setState({ status: "ready", page: { artifacts: [...state.page.artifacts, ...next.artifacts], next_cursor: next.next_cursor } });
    } catch (error) {
      if (generation.current !== requestGeneration) return;
      setLoadMoreError(error instanceof Error ? error.message : "Unable to load more artifacts.");
    } finally {
      if (generation.current === requestGeneration) setLoadingMore(false);
    }
  };

  if (state.status === "failed" && state.notFound) return <NotFoundPage />;

  return <>
    <PageHeader title="Artifacts" description="Source maps, ProGuard/R8 mappings, and native debug files currently available for symbolication." />
    <div className="space-y-4 p-4 sm:p-6">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">Type</span>
        <Select value={filter} onValueChange={(value) => setFilter(value as Filter)}>
          <SelectTrigger aria-label="Artifact type"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All types</SelectItem>
            <SelectItem value="source_map">Source maps</SelectItem>
            <SelectItem value="proguard_mapping">ProGuard / R8</SelectItem>
            <SelectItem value="native_debug_file">Native debug files</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {state.status === "loading" && <div className="space-y-2">{[0, 1, 2].map((row) => <Skeleton key={row} className="h-16 w-full" />)}</div>}
      {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void loadFirst()} />}
      {state.status === "ready" && state.page.artifacts.length === 0 && <EmptyState icon={Boxes} title={filter === "all" ? "No artifacts yet" : "No matching artifacts"} description={emptyDescription(filter)} />}
      {state.status === "ready" && state.page.artifacts.length > 0 && <>
        <ArtifactTable artifacts={state.page.artifacts} />
        {loadMoreError !== null && <ErrorState className="py-6" title="Could not load more" message={loadMoreError} onRetry={() => void loadMore()} />}
        {state.page.next_cursor !== null && loadMoreError === null && <div className="flex justify-center"><Button variant="outline" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "Loading…" : "Load more"}</Button></div>}
      </>}
    </div>
  </>;
}

function ArtifactTable({ artifacts }: { artifacts: readonly ProjectArtifact[] }) {
  return <div className="rounded-md border"><Table className={STACKED_ROWS_BELOW_MD}>
    <TableHeader><TableRow><TableHead>Type</TableHead><TableHead>Identifier / references</TableHead><TableHead>Size</TableHead><TableHead>Uploaded</TableHead><TableHead>Last used</TableHead></TableRow></TableHeader>
    <TableBody>{artifacts.map((artifact) => <TableRow key={artifactKey(artifact)}>
      <TableCell><Badge variant="outline">{typeLabel(artifact.type)}</Badge></TableCell>
      <TableCell className="max-w-xl whitespace-normal"><ArtifactIdentity artifact={artifact} /></TableCell>
      <TableCell><MobileLabel>Size</MobileLabel>{formatBytes(artifact.size_bytes)}</TableCell>
      <TableCell><MobileLabel>Uploaded</MobileLabel><RelativeTime iso={artifact.uploaded_at} /></TableCell>
      <TableCell><MobileLabel>Last used</MobileLabel>{artifact.last_used_at === null ? "—" : <RelativeTime iso={artifact.last_used_at} />}</TableCell>
    </TableRow>)}</TableBody>
  </Table></div>;
}

function MobileLabel({ children }: { children: string }) {
  return <span aria-hidden="true" className="mr-2 font-medium md:hidden">{children}</span>;
}

function ArtifactIdentity({ artifact }: { artifact: ProjectArtifact }) {
  if (artifact.type === "proguard_mapping") return <code className="break-all text-xs">{artifact.mapping_uuid}</code>;
  if (artifact.type === "source_map") return <div className="space-y-1"><code className="break-all text-xs">sha256:{artifact.content_sha256}</code><ul className="text-muted-foreground space-y-0.5 text-xs">
    {artifact.references.map((ref, index) => <li key={`${ref.debug_id ?? ref.generated_path ?? "ref"}-${index}`}>{sourceReference(ref)}</li>)}
  </ul></div>;
  return <div className="space-y-1"><code className="break-all text-xs">sha256:{artifact.content_sha256}</code><ul className="text-muted-foreground space-y-0.5 text-xs">
    {artifact.references.map((ref) => <li key={ref.debug_id}>{ref.debug_id} · {ref.cpu_name}</li>)}
  </ul></div>;
}

function sourceReference(ref: { release?: string; generated_path?: string; debug_id?: string }) {
  if (ref.debug_id) return `Debug ID ${ref.debug_id}`;
  return `${ref.release ?? "Unknown release"} · ${ref.generated_path ?? "Unknown path"}`;
}

function artifactKey(artifact: ProjectArtifact) { return artifact.type === "proguard_mapping" ? `${artifact.type}:${artifact.mapping_uuid}` : `${artifact.type}:${artifact.content_sha256}`; }
function typeLabel(type: ArtifactType) { return type === "source_map" ? "Source map" : type === "proguard_mapping" ? "ProGuard / R8" : "Native debug file"; }
function formatBytes(bytes: number) { if (bytes < 1024) return `${bytes} B`; const units = ["KB", "MB", "GB"]; let value = bytes / 1024; let unit = units[0]; for (let index = 1; index < units.length && value >= 1024; index++) { value /= 1024; unit = units[index]; } return `${value.toFixed(value >= 10 ? 0 : 1)} ${unit}`; }
function emptyDescription(filter: Filter) { if (filter === "source_map") return "Upload source maps with sentry-cli or a supported bundler plugin to make them available here."; if (filter === "proguard_mapping") return "Upload an Android ProGuard or R8 mapping to make it available here."; if (filter === "native_debug_file") return "Upload native debug information with sentry-cli to make it available here."; return "Upload source maps, ProGuard/R8 mappings, or native debug files with an artifact token to make them available here."; }
