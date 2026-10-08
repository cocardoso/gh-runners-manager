import { screen, within } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";
import type { Repository } from "@/api/client";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const recent = new Date(Date.now() - 5 * 60_000).toISOString();

function repository(over: Partial<Repository> = {}): Repository {
  return {
    url: "https://github.com/octo/app",
    owner: "octo",
    repo: "app",
    kind: "repository",
    scale_sets: ["homelab"],
    credentials: ["personal"],
    jobs_running: 0,
    jobs_24h: 0,
    succeeded_24h: 0,
    failed_24h: 0,
    ...over,
  };
}

const data = {
  repositories: [
    repository({
      url: "https://github.com/acme",
      owner: "acme",
      repo: "",
      kind: "organization",
      scale_sets: ["acme-big", "acme-small"],
      credentials: ["org-token"],
      jobs_running: 2,
      jobs_24h: 4,
      succeeded_24h: 3,
      failed_24h: 1,
      repositories_seen: ["acme/api", "acme/web"],
      last_job: { id: "j9", display_name: "deploy", status: "completed", result: "failed", repository: "acme/web", updated_at: recent, finished_at: recent },
    }),
    repository({ jobs_24h: 0, last_job: undefined }),
  ],
};

test("lists each repository and organization with its scale sets, credential and recent jobs", async () => {
  mockApi({ "/api/v1/repositories": data });
  renderApp("/repositories");
  expect(await screen.findByRole("heading", { name: "Repositories", level: 1 })).toBeInTheDocument();
  expect(screen.getByText(/repositories and organizations the scale sets serve/)).toBeInTheDocument();

  const org = (await screen.findByRole("link", { name: "acme" })).closest("tr")!;
  expect(screen.getByRole("link", { name: "acme" })).toHaveAttribute("href", "https://github.com/acme");
  expect(within(org).getByText("Organization")).toBeInTheDocument();
  expect(within(org).getByText(/acme\/api/)).toBeInTheDocument();
  expect(within(org).getByRole("link", { name: "acme-big" })).toHaveAttribute("href", "/scale-sets");
  expect(within(org).getByText("org-token")).toBeInTheDocument();
  expect(within(org).getByText("2")).toBeInTheDocument();
  expect(within(org).getByText("4 jobs")).toBeInTheDocument();
  expect(within(org).getByText(/75%/)).toBeInTheDocument();
  expect(within(org).getByRole("link", { name: "deploy" })).toHaveAttribute("href", "/jobs/j9");
  expect(within(org).getByText("failed")).toBeInTheDocument();

  const repo = screen.getByRole("link", { name: "octo/app" }).closest("tr")!;
  expect(within(repo).queryByText("Organization")).not.toBeInTheDocument();
  expect(within(repo).getByText("No jobs")).toBeInTheDocument();
  expect(within(repo).getByText("Never")).toBeInTheDocument();
});

test("without scale sets it points to the scale sets page", async () => {
  mockApi({ "/api/v1/repositories": { repositories: [] } });
  renderApp("/repositories");
  expect(await screen.findByText("No repository yet")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Go to scale sets" })).toHaveAttribute("href", "/scale-sets");
});

test("in Portuguese the page is titled Repositórios", async () => {
  mockApi({ "/api/v1/repositories": data });
  renderApp("/repositories", { locale: "pt-BR" });
  expect(await screen.findByRole("heading", { name: "Repositórios", level: 1 })).toBeInTheDocument();
  expect(await screen.findByText("Organização")).toBeInTheDocument();
  expect(screen.getByText("4 jobs")).toBeInTheDocument();
});

test("the counts lead to the matching jobs, and an organization's hidden repositories are counted in full", async () => {
  const repo = repository({ jobs_running: 1, jobs_24h: 3, succeeded_24h: 3 });
  const org = repository({
    url: "https://github.com/acme",
    owner: "acme",
    repo: "",
    kind: "organization",
    scale_sets: ["acme-big"],
    jobs_running: 2,
    jobs_24h: 4,
    succeeded_24h: 4,
    repositories_seen: Array.from({ length: 50 }, (_, i) => `acme/r${i}`),
    repositories_seen_total: 60,
  });
  mockApi({ "/api/v1/repositories": { repositories: [org, repo] } });
  renderApp("/repositories");
  const repoRow = (await screen.findByRole("link", { name: "octo/app" })).closest("tr")!;
  expect(within(repoRow).getByRole("link", { name: "1" }).getAttribute("href")).toBe("/jobs?repo=octo%2Fapp");
  expect(within(repoRow).getByRole("link", { name: /3 jobs/ }).getAttribute("href")).toBe("/jobs?tab=history&repo=octo%2Fapp&range=24h");
  const orgRow = screen.getByRole("link", { name: "acme" }).closest("tr")!;
  expect(within(orgRow).getByRole("link", { name: "2" }).getAttribute("href")).toBe("/jobs?scale_set=acme-big");
  expect(within(orgRow).getByText(/\+55 more/)).toBeInTheDocument(); // 60 seen, 5 shown
});
