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
  listChannels: vi.fn(),
  createPushJob: vi.fn(),
}));

import { createPushJob, getPool, listChannels, listPushJobs } from "@/api/admin";
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
  vi.mocked(listChannels).mockResolvedValue({
    channels: [
      { channel_id: "tg1", channel_type: "telegram", name: "Main", status: "active", configured: true },
      { channel_id: "tg-off", channel_type: "telegram", name: "Old", status: "disabled", configured: true },
    ],
  });
  vi.mocked(createPushJob).mockResolvedValue({ JobID: "j1" } as never);
});

// The dialog has two selects: Channel (combobox 0) and Target tier (combobox 1).
const channelSelect = () => screen.getAllByRole("combobox")[0];
const tierSelect = () => screen.getAllByRole("combobox")[1];

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /new push/i }));
  // The selects only exist once the dialog is open (wait for both).
  await screen.findByRole("button", { name: /create push/i });
}

async function fillBasics(user: ReturnType<typeof userEvent.setup>) {
  await user.type(document.querySelector('input[name="title"]')!, "Hello");
  await user.type(document.querySelector('textarea[name="content"]')!, "Body");
}

describe("PushPage tier + channel gate (TC-11 / TC-15)", () => {
  it("blocks submit when no tier is selected", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await fillBasics(user);
    await user.selectOptions(channelSelect(), "tg1"); // channel picked, tier left blank
    await user.click(screen.getByRole("button", { name: /create push/i }));

    // required validation blocks the submit → the mutation never fires.
    await waitFor(() => expect(screen.getByText(/pick a tier/i)).toBeInTheDocument());
    expect(createPushJob).not.toHaveBeenCalled();
  });

  it("blocks submit when no channel is selected (TC-15)", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await fillBasics(user);
    await user.selectOptions(tierSelect(), "gold"); // tier picked, channel left blank
    await user.click(screen.getByRole("button", { name: /create push/i }));

    await waitFor(() => expect(screen.getByText(/pick a channel/i)).toBeInTheDocument());
    expect(createPushJob).not.toHaveBeenCalled();
  });

  it("no longer exposes topic / entitlement inputs (TC-16)", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);
    expect(document.querySelector('input[name="topic"]')).toBeNull();
    expect(document.querySelector('input[name="entitlement"]')).toBeNull();
  });

  it("only offers active instances (disabled ones are excluded, TC-15)", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);
    const opts = Array.from(channelSelect().querySelectorAll("option")).map((o) => o.textContent);
    expect(opts).toContain("Main — telegram");
    expect(opts).not.toContain("Old — telegram"); // disabled instance excluded
  });

  it("sends channel_id + channel_type and no target.tier when 'All members' is chosen", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await fillBasics(user);
    await user.selectOptions(channelSelect(), "tg1");
    await user.selectOptions(tierSelect(), "__all__");
    await user.click(screen.getByRole("button", { name: /create push/i }));

    await waitFor(() => expect(createPushJob).toHaveBeenCalledTimes(1));
    const body = vi.mocked(createPushJob).mock.calls[0][0];
    expect(body.channel_id).toBe("tg1");
    expect(body.channel_type).toBe("telegram");
    expect(body.target).toEqual({}); // no tier gate
  });

  it("sends the chosen tier as target.tier", async () => {
    const user = userEvent.setup();
    renderPage();
    await openDialog(user);

    await fillBasics(user);
    await user.selectOptions(channelSelect(), "tg1");
    await user.selectOptions(tierSelect(), "gold");
    await user.click(screen.getByRole("button", { name: /create push/i }));

    await waitFor(() => expect(createPushJob).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createPushJob).mock.calls[0][0].target).toEqual({ tier: "gold" });
  });
});
