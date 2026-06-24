import {
  AppWindow,
  BellRing,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Megaphone,
  ScrollText,
  Send,
  Settings,
  SlidersHorizontal,
  Users,
  type LucideIcon,
} from "lucide-react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "@/auth/AuthContext";
import { roleRank } from "@/lib/config";
import type { Role } from "@/lib/types";
import { cn } from "@/lib/cn";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";

interface NavItem {
  to: string;
  label: string;
  min: Role;
  icon: LucideIcon;
}

const NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", min: "viewer", icon: LayoutDashboard },
  { to: "/members", label: "Members", min: "viewer", icon: Users },
  { to: "/subscriptions", label: "Subscriptions", min: "viewer", icon: BellRing },
  { to: "/rules", label: "Rules", min: "operator", icon: SlidersHorizontal },
  { to: "/channels", label: "Channels", min: "operator", icon: Send },
  { to: "/push", label: "Push", min: "operator", icon: Megaphone },
  { to: "/clients", label: "OAuth Clients", min: "owner", icon: AppWindow },
  { to: "/keys", label: "Signing Keys", min: "owner", icon: KeyRound },
  { to: "/audit", label: "Audit", min: "owner", icon: ScrollText },
  { to: "/setup", label: "Setup", min: "owner", icon: Settings },
];

export function Layout() {
  const { role, logout } = useAuth();
  const navigate = useNavigate();
  const rank = role ? roleRank[role] : 0;
  const items = NAV.filter((n) => rank >= roleRank[n.min]);

  async function onLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className="grid min-h-dvh grid-cols-[15rem_1fr]">
      <aside className="flex flex-col gap-1 border-r p-3">
        <div className="flex items-center gap-2 px-2 py-3">
          <div className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground text-xs font-bold">
            OP
          </div>
          <span className="font-semibold">Ouro Pass</span>
        </div>
        <nav className="flex flex-col gap-0.5">
          {items.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive ? "bg-primary/10 font-medium text-primary" : "hover:bg-muted",
                )
              }
            >
              <n.icon className="h-4 w-4" />
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto flex items-center justify-between px-1 pt-3">
          <Badge variant="muted" className="capitalize">
            {role ?? "—"}
          </Badge>
          <Button variant="ghost" size="icon" onClick={onLogout} title="Log out">
            <LogOut className="h-4 w-4" />
          </Button>
        </div>
      </aside>
      <main className="overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  );
}
