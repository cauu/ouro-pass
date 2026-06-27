import {
  AppWindow,
  BellRing,
  KeyRound,
  LayoutDashboard,
  Lock,
  LogOut,
  Megaphone,
  Menu,
  Moon,
  ScrollText,
  Send,
  SlidersHorizontal,
  Sun,
  Users,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
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

// Grouped navigation (S0007): five sections give the destinations structure and
// let RBAC-gated items cluster meaningfully. A group renders only when the
// current role can see at least one of its items.
const NAV_GROUPS: { group: string; items: NavItem[] }[] = [
  {
    group: "Overview",
    items: [{ to: "/dashboard", label: "Dashboard", min: "viewer", icon: LayoutDashboard }],
  },
  {
    group: "Identities",
    items: [
      { to: "/members", label: "Members", min: "viewer", icon: Users },
      { to: "/subscriptions", label: "Subscriptions", min: "viewer", icon: BellRing },
    ],
  },
  {
    group: "Access & Rules",
    items: [
      { to: "/clients", label: "OAuth Clients", min: "owner", icon: AppWindow },
      { to: "/eligibility", label: "Eligibility", min: "operator", icon: SlidersHorizontal },
    ],
  },
  {
    group: "Delivery",
    items: [
      { to: "/channels", label: "Channels", min: "operator", icon: Send },
      { to: "/push", label: "Push", min: "operator", icon: Megaphone },
    ],
  },
  {
    group: "Security",
    items: [
      { to: "/keys", label: "Signing Keys", min: "owner", icon: KeyRound },
      { to: "/audit", label: "Audit log", min: "owner", icon: ScrollText },
    ],
  },
];

const THEME_KEY = "op-admin-theme";

function useTheme(): [boolean, () => void] {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  useEffect(() => {
    try {
      const stored = localStorage.getItem(THEME_KEY);
      if (stored) {
        const isDark = stored === "dark";
        setDark(isDark);
        document.documentElement.classList.toggle("dark", isDark);
      }
    } catch {
      /* localStorage unavailable — keep current DOM state */
    }
  }, []);
  const toggle = () => {
    setDark((prev) => {
      const next = !prev;
      document.documentElement.classList.toggle("dark", next);
      try {
        localStorage.setItem(THEME_KEY, next ? "dark" : "light");
      } catch {
        /* ignore */
      }
      return next;
    });
  };
  return [dark, toggle];
}

export function Layout() {
  const { role, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [dark, toggleTheme] = useTheme();
  // Narrow-screen nav drawer (S0007 p4-2): the sidebar is a static column on md+
  // and an overlay drawer below md, opened from the header hamburger.
  const [navOpen, setNavOpen] = useState(false);
  useEffect(() => setNavOpen(false), [location.pathname]);
  const rank = role ? roleRank[role] : 0;

  const visibleGroups = NAV_GROUPS.map((g) => ({
    ...g,
    items: g.items.filter((n) => rank >= roleRank[n.min]),
  })).filter((g) => g.items.length > 0);

  // Breadcrumb derives from the active route's group + label.
  const current = NAV_GROUPS.flatMap((g) => g.items.map((i) => ({ ...i, group: g.group }))).find(
    (i) => location.pathname.startsWith(i.to),
  );

  async function onLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className="min-h-dvh md:grid md:grid-cols-[15rem_1fr]">
      {navOpen ? (
        <div
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          aria-hidden
          onClick={() => setNavOpen(false)}
        />
      ) : null}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r bg-card transition-transform md:static md:z-auto md:w-auto md:translate-x-0",
          navOpen ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <div className="grid h-7 w-7 place-items-center rounded-md bg-primary text-xs font-bold text-primary-foreground">
            OP
          </div>
          <span className="font-semibold tracking-tight">Ouro Pass</span>
        </div>
        <nav className="flex flex-1 flex-col gap-4 overflow-y-auto p-3">
          {visibleGroups.map((g) => (
            <div key={g.group} className="flex flex-col gap-0.5">
              <div className="px-3 pb-1 text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground/80">
                {g.group}
              </div>
              {g.items.map((n) => (
                <NavLink
                  key={n.to}
                  to={n.to}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary",
                      isActive
                        ? "bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )
                  }
                >
                  <n.icon className="h-4 w-4 shrink-0" />
                  <span className="flex-1 truncate">{n.label}</span>
                  {n.min === "owner" ? <Lock className="h-3 w-3 opacity-40" /> : null}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="flex items-center gap-2 border-t px-3 py-3">
          <div className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-muted text-[11px] font-semibold text-muted-foreground">
            {role ? role.slice(0, 2).toUpperCase() : "—"}
          </div>
          <Badge variant="muted" className="capitalize">
            {role ?? "—"}
          </Badge>
          <Button
            variant="ghost"
            size="icon"
            className="ml-auto"
            onClick={onLogout}
            title="Log out"
          >
            <LogOut className="h-4 w-4" />
          </Button>
        </div>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-14 items-center gap-3 border-b bg-card/60 px-4 backdrop-blur md:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="md:hidden"
            onClick={() => setNavOpen(true)}
            title="Open menu"
          >
            <Menu className="h-4 w-4" />
          </Button>
          <div className="flex items-center gap-2 text-sm">
            {current ? (
              <>
                <span className="text-muted-foreground">{current.group}</span>
                <span className="text-muted-foreground/50">/</span>
                <span className="font-medium">{current.label}</span>
              </>
            ) : null}
          </div>
          <div className="flex-1" />
          <Button
            variant="ghost"
            size="icon"
            onClick={toggleTheme}
            title={dark ? "Switch to light" : "Switch to dark"}
          >
            {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>
        </header>
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
