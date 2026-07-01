import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

// S0019 p3-3 / TC-11: the push-modal tier gate. Target tier is a REQUIRED select;
// a blank (unselected) submit must be blocked, and the explicit "All members" choice
// must send NO target.tier — so a broadcast-to-all is a conscious selection, never
// the silent default.

vi.mock("@/ui/toast", () => ({ useToast: () => ({ toast: vi.fn() }) }));
vi.mock("@/api/admin", () => ({
  getPool: vi.fn(),
  listPushJobs: vi.fn(),
  createPushJob: vi.fn(),
}));

import { createPushJob, getPool, listPushJobs } from "@/api/admin";
import { PushPage } from "./PushPage";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <PushPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(listPushJobs).mockResolvedValue({ jobs: [] });
  vi.mocked(getPool).mockResolvedValue({
    pool_id: "p",
    tier_rules: [{ tier: "gold" }, { tier: "silver" }],
  });
  vi.mocked(createPushJob).mockResolvedValue({ JobID: "j1" } as never);
});

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /new push/i }));
  // The tier select only exists once the dialog is open.
  await screen.findByRole("combobox");
}

describe("PushPage tier gate (TC-11)", () => {
  it("blocks submit when no tier is selected", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await user.type(document.querySelector('input[name="title"]')!, "Hello");
    await user.type(document.querySelector('textarea[name="content"]')!, "Body");
    // Leave the tier select on its disabled "" placeholder.
    await user.click(screen.getByRole("button", { name: /create push/i }));

    // required validation blocks the submit → the mutation never fires.
    await waitFor(() => expect(screen.getByText(/pick a tier/i)).toBeInTheDocument());
    expect(createPushJob).not.toHaveBeenCalled();
  });

  it("sends no target.tier when 'All members' is chosen", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await user.type(document.querySelector('input[name="title"]')!, "Hello");
    await user.type(document.querySelector('textarea[name="content"]')!, "Body");
    await user.selectOptions(screen.getByRole("combobox"), "__all__");
    await user.click(screen.getByRole("button", { name: /create push/i }));

    await waitFor(() => expect(createPushJob).toHaveBeenCalledTimes(1));
    const body = vi.mocked(createPushJob).mock.calls[0][0];
    expect(body.target).toEqual({}); // no tier gate
    expect(body.target.tier).toBeUndefined();
  });

  it("sends the chosen tier as target.tier", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await user.type(document.querySelector('input[name="title"]')!, "Hello");
    await user.type(document.querySelector('textarea[name="content"]')!, "Body");
    await user.selectOptions(screen.getByRole("combobox"), "gold");
    await user.click(screen.getByRole("button", { name: /create push/i }));

    await waitFor(() => expect(createPushJob).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createPushJob).mock.calls[0][0].target).toEqual({ tier: "gold" });
  });
});
