import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Role } from "@/lib/types";
import { RequireRole } from "./guards";

vi.mock("@/auth/AuthContext", () => ({ useAuth: vi.fn() }));
import { useAuth } from "@/auth/AuthContext";

function setRole(role: Role | null) {
  vi.mocked(useAuth).mockReturnValue({
    role,
    me: role ? { admin_id: "a", role } : null,
    loading: false,
    login: vi.fn(),
    logout: vi.fn(),
  });
}

describe("RequireRole (RBAC gate, TC-2)", () => {
  it("renders children when the role rank meets the minimum", () => {
    setRole("owner");
    render(
      <RequireRole min="operator">
        <div>secret</div>
      </RequireRole>,
    );
    expect(screen.getByText("secret")).toBeInTheDocument();
  });

  it("blocks when the role rank is below the minimum", () => {
    setRole("viewer");
    render(
      <RequireRole min="operator">
        <div>secret</div>
      </RequireRole>,
    );
    expect(screen.queryByText("secret")).not.toBeInTheDocument();
    expect(screen.getByText(/requires the/i)).toBeInTheDocument();
  });

  it("treats an equal role as sufficient", () => {
    setRole("operator");
    render(
      <RequireRole min="operator">
        <div>ok</div>
      </RequireRole>,
    );
    expect(screen.getByText("ok")).toBeInTheDocument();
  });
});
