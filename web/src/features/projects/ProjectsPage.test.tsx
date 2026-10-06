import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: "owner" } }
  })
}));

const listed = vi.hoisted(() => ({ projects: [] as Array<Record<string, unknown>> }));

vi.mock("@/features/projects/ProjectsProvider", () => ({
  useProjects: () => ({
    state: { status: "ready", projects: listed.projects },
    reload: vi.fn().mockResolvedValue(undefined)
  })
}));

vi.mock("@/apiClient", () => ({
  api: { createProject: vi.fn() }
}));

import { api } from "@/apiClient";

import { ProjectsPage } from "./ProjectsPage";

const mockedCreateProject = vi.mocked(api.createProject);

// Radix Select opens on pointerdown and probes pointer capture and scrolling
// APIs that jsdom does not implement.
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

const PICKER_OPTIONS = [
  { value: "javascript", label: "JavaScript" },
  { value: "nestjs", label: "NestJS" },
  { value: "python", label: "Python" },
  { value: "python-django", label: "Python · Django" },
  { value: "python-fastapi", label: "Python · FastAPI" },
  { value: "python-flask", label: "Python · Flask" },
  { value: "go", label: "Go" },
  { value: "ruby", label: "Ruby" },
  { value: "ruby-rails", label: "Ruby · Rails" },
  { value: "php", label: "PHP" },
  { value: "php-laravel", label: "PHP · Laravel" },
  { value: "php-symfony", label: "PHP · Symfony" },
  { value: "rust", label: "Rust" },
  { value: "jvm-spring", label: "JVM · Spring" },
  { value: "jvm-ktor", label: "JVM · Ktor" },
  { value: "kotlin-multiplatform", label: "Kotlin Multiplatform" },
  { value: "scala", label: "JVM · Scala" },
  { value: "android", label: "Android" },
  { value: "ios", label: "iOS" },
  { value: "other", label: "Other" }
];

function openCreateDialog() {
  render(
    <MemoryRouter>
      <ProjectsPage />
    </MemoryRouter>
  );
  fireEvent.click(screen.getAllByRole("button", { name: "New project" })[0]);
}

function openPlatformPicker() {
  const trigger = screen.getByRole("combobox", { name: "Platform" });
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
  return trigger;
}

describe("ProjectsPage project list", () => {
  beforeEach(() => {
    listed.projects = [
      { id: 1, slug: "shop-app", name: "Shop App", platform: "kotlin-multiplatform", created_at: "2026-07-13T08:00:00Z" },
      { id: 2, slug: "legacy", name: "Legacy", platform: "cobol", created_at: "2026-07-13T08:00:00Z" }
    ];
  });

  afterEach(() => {
    listed.projects = [];
  });

  it("shows the platform label rather than the stored value", () => {
    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>
    );

    expect(screen.getByText("Kotlin Multiplatform")).toBeTruthy();
    expect(screen.queryByText("kotlin-multiplatform")).toBeNull();
  });

  it("shows a stored value that is not a known platform as it is", () => {
    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>
    );

    expect(screen.getByText("cobol")).toBeTruthy();
  });
});

describe("ProjectsPage create-project platform picker", () => {
  beforeEach(() => {
    mockedCreateProject.mockReset();
  });

  it("preselects JavaScript", () => {
    openCreateDialog();

    expect(screen.getByRole("combobox", { name: "Platform" }).textContent).toBe("JavaScript");
  });

  it("offers every platform with its label", () => {
    openCreateDialog();
    openPlatformPicker();

    const options = within(screen.getByRole("listbox")).getAllByRole("option");

    expect(options.map((option) => option.textContent)).toEqual(PICKER_OPTIONS.map((option) => option.label));
  });

  it.each(PICKER_OPTIONS.filter((option) => option.value !== "javascript"))(
    "submits $value when $label is picked",
    ({ value, label }) => {
      mockedCreateProject.mockResolvedValue({
        id: 1,
        slug: "app",
        name: "App",
        platform: value,
        created_at: "2026-07-13T08:00:00Z"
      });
      openCreateDialog();
      fireEvent.change(screen.getByLabelText("Name"), { target: { value: "App" } });
      fireEvent.change(screen.getByLabelText("Slug"), { target: { value: "app" } });
      openPlatformPicker();
      fireEvent.click(screen.getByRole("option", { name: label }));

      fireEvent.click(screen.getByRole("button", { name: "Create project" }));

      expect(mockedCreateProject).toHaveBeenCalledWith({ slug: "app", name: "App", platform: value });
    }
  );
});

describe("ProjectsPage create-project slug", () => {
  beforeEach(() => {
    mockedCreateProject.mockReset();
  });

  it("suggests a slug from the name as it is typed", () => {
    openCreateDialog();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shop App" } });

    expect((screen.getByLabelText("Slug") as HTMLInputElement).value).toBe("shop-app");
  });

  it("stops following the name once the slug has been edited by hand", () => {
    openCreateDialog();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shop" } });

    fireEvent.change(screen.getByLabelText("Slug"), { target: { value: "storefront" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shop App" } });

    expect((screen.getByLabelText("Slug") as HTMLInputElement).value).toBe("storefront");
  });

  it("follows the name again after the slug is cleared", () => {
    openCreateDialog();
    fireEvent.change(screen.getByLabelText("Slug"), { target: { value: "storefront" } });
    fireEvent.change(screen.getByLabelText("Slug"), { target: { value: "" } });

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shop App" } });

    expect((screen.getByLabelText("Slug") as HTMLInputElement).value).toBe("shop-app");
  });

  it("opens the new project's ingest keys after creating it, since a project cannot receive events without one", async () => {
    mockedCreateProject.mockResolvedValue({
      id: 9,
      slug: "shop-app",
      name: "Shop App",
      platform: "javascript",
      created_at: "2026-07-13T08:00:00Z"
    });
    render(
      <MemoryRouter>
        <Routes>
          <Route path="/" element={<ProjectsPage />} />
          <Route path="/:slug/settings/keys" element={<p>keys screen</p>} />
        </Routes>
      </MemoryRouter>
    );
    fireEvent.click(screen.getAllByRole("button", { name: "New project" })[0]);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shop App" } });

    fireEvent.click(screen.getByRole("button", { name: "Create project" }));

    expect(await screen.findByText("keys screen")).toBeTruthy();
  });
});
