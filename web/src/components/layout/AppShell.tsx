import { useQueryClient } from "@tanstack/react-query";
import {
  BookOpen,
  GitCompareArrows,
  LayoutDashboard,
  List,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Plus,
  Search,
  Settings,
  Sun,
  Webhook,
  X,
} from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { NavLink, Outlet, useLocation, useMatch, useNavigate, useSearchParams } from "react-router";
import { useLive } from "../../hooks/live";
import { useTheme, type Theme } from "../../hooks/useTheme";
import { api } from "../../lib/api";
import { cn } from "../../lib/cn";
import { useEndpoints, useMeta } from "../../lib/queries";
import { Logo } from "../Logo";
import { useToast } from "../toast";
import { LinkButton } from "../ui/button";
import { Kbd } from "../ui/misc";

const nav = [
  { to: "/", label: "Overview", icon: LayoutDashboard, end: true },
  { to: "/endpoints", label: "Endpoints", icon: Webhook, end: false },
  { to: "/requests", label: "Requests", icon: List, end: false },
  { to: "/compare", label: "Compare", icon: GitCompareArrows, end: false },
  { to: "/settings", label: "Settings", icon: Settings, end: false },
  { to: "/docs", label: "Documentation", icon: BookOpen, end: false },
];

export function AppShell() {
  const [navOpen, setNavOpen] = useState(false);
  return (
    <div className="flex min-h-dvh">
      <a
        href="#main"
        className="sr-only z-50 rounded bg-accent px-3 py-2 text-white focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        Skip to content
      </a>
      <Sidebar open={navOpen} onNavigate={() => setNavOpen(false)} />
      {navOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 md:hidden"
          onClick={() => setNavOpen(false)}
          aria-hidden
        />
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onMenu={() => setNavOpen(true)} />
        <main id="main" className="min-w-0 flex-1 px-4 py-5 sm:px-6 lg:px-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function Sidebar({ open, onNavigate }: { open: boolean; onNavigate: () => void }) {
  const meta = useMeta();
  return (
    <div className="md:w-60 md:shrink-0 md:bg-sidebar">
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-60 flex-col bg-sidebar text-sidebar-fg transition-transform md:sticky md:top-0 md:h-dvh md:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
        aria-label="Main navigation"
      >
        <div className="flex h-14 items-center justify-between px-4">
          <Logo />
          <button
            type="button"
            onClick={onNavigate}
            className="rounded p-1 text-sidebar-muted hover:text-white md:hidden"
            aria-label="Close navigation"
          >
            <X className="size-4" aria-hidden />
          </button>
        </div>
        <EndpointSwitcher onNavigate={onNavigate} />
        <nav className="mt-3 flex flex-col gap-0.5 px-2">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              onClick={onNavigate}
              className={({ isActive }) =>
                cn(
                  "flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] transition-colors",
                  isActive
                    ? "bg-sidebar-active font-medium text-white"
                    : "text-sidebar-fg/85 hover:bg-sidebar-raised hover:text-white",
                )
              }
            >
              <Icon className="size-4 opacity-80" aria-hidden />
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto space-y-3 border-t border-sidebar-line px-4 py-3">
          <ThemeSelect />
          <div className="flex items-center justify-between text-xs text-sidebar-muted">
            <span
              className="font-mono"
              title={meta.data ? `Commit ${meta.data.commit}, built ${meta.data.build_date}` : undefined}
            >
              {meta.data ? `v${meta.data.version.replace(/^v/, "")}` : "\u00a0"}
            </span>
            {meta.data?.auth_required && <LogoutButton />}
          </div>
        </div>
      </aside>
    </div>
  );
}

function EndpointSwitcher({ onNavigate }: { onNavigate: () => void }) {
  const { data: endpoints } = useEndpoints();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const onRequests = useMatch("/requests");
  const onEndpoint = useMatch("/endpoints/:id");
  const current = onRequests ? (params.get("endpoint_id") ?? "") : (onEndpoint?.params.id ?? "");

  return (
    <div className="px-3">
      <label
        htmlFor="endpoint-switcher"
        className="mb-1 block px-1 text-[11px] font-medium tracking-wide text-sidebar-muted uppercase"
      >
        Endpoint
      </label>
      <select
        id="endpoint-switcher"
        value={endpoints?.some((e) => String(e.id) === current) ? current : ""}
        onChange={(e) => {
          navigate(e.target.value ? `/requests?endpoint_id=${e.target.value}` : "/requests");
          onNavigate();
        }}
        className="h-8 w-full cursor-pointer rounded-md border border-sidebar-line bg-sidebar-raised px-2 text-[13px] text-sidebar-fg focus:border-accent focus:outline-none"
      >
        <option value="">All endpoints</option>
        {endpoints?.map((e) => (
          <option key={e.id} value={e.id}>
            {e.name}
            {e.enabled ? "" : " (disabled)"}
          </option>
        ))}
      </select>
    </div>
  );
}

const themes: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: "system", label: "System", icon: Monitor },
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
];

function ThemeSelect() {
  const [theme, setTheme] = useTheme();
  return (
    <div role="radiogroup" aria-label="Theme" className="flex rounded-md border border-sidebar-line p-0.5">
      {themes.map(({ value, label, icon: Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          onClick={() => setTheme(value)}
          title={`${label} theme`}
          className={cn(
            "flex h-6 flex-1 items-center justify-center gap-1.5 rounded text-[11px] transition-colors",
            theme === value ? "bg-sidebar-active text-white" : "text-sidebar-muted hover:text-white",
          )}
        >
          <Icon className="size-3.5" aria-hidden />
          {label}
        </button>
      ))}
    </div>
  );
}

function LogoutButton() {
  const qc = useQueryClient();
  const toast = useToast();
  return (
    <button
      type="button"
      onClick={async () => {
        try {
          await api.logout();
        } catch {
          toast.error("Signing out failed. Try again.");
          return;
        }
        qc.clear();
        qc.setQueryData(["session"], { auth_required: true, authenticated: false });
      }}
      className="inline-flex items-center gap-1 rounded px-1 py-0.5 hover:text-white"
    >
      <LogOut className="size-3.5" aria-hidden />
      Sign out
    </button>
  );
}

function TopBar({ onMenu }: { onMenu: () => void }) {
  return (
    <header className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-3 border-b border-line bg-surface/95 px-4 backdrop-blur sm:px-6 lg:px-8">
      <button
        type="button"
        onClick={onMenu}
        className="-ml-1 rounded p-1.5 text-muted hover:text-fg md:hidden"
        aria-label="Open navigation"
      >
        <Menu className="size-5" aria-hidden />
      </button>
      <GlobalSearch />
      <div className="ml-auto flex items-center gap-4">
        <LiveIndicator />
        <LinkButton
          to="/endpoints?new=1"
          variant="primary"
          size="sm"
          icon={<Plus className="size-3.5" aria-hidden />}
          className="hidden sm:inline-flex"
        >
          New endpoint
        </LinkButton>
      </div>
    </header>
  );
}

function GlobalSearch() {
  const location = useLocation();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const onRequests = location.pathname === "/requests";
  const urlQ = onRequests ? (params.get("q") ?? "") : "";
  const [value, setValue] = useState(urlQ);
  const [lastUrlQ, setLastUrlQ] = useState(urlQ);
  const inputRef = useRef<HTMLInputElement>(null);

  if (urlQ !== lastUrlQ) {
    setLastUrlQ(urlQ);
    setValue(urlQ);
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(t.tagName))) return;
      e.preventDefault();
      inputRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const q = value.trim().slice(0, 200);
    if (onRequests) {
      const next = new URLSearchParams(params);
      if (q) next.set("q", q);
      else next.delete("q");
      next.delete("offset");
      setParams(next);
    } else {
      navigate(q ? `/requests?q=${encodeURIComponent(q)}` : "/requests");
    }
  };

  return (
    <form role="search" onSubmit={submit} className="relative w-full max-w-md">
      <Search
        className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-faint"
        aria-hidden
      />
      <input
        ref={inputRef}
        type="search"
        value={value}
        maxLength={200}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") inputRef.current?.blur();
        }}
        placeholder="Search paths, headers, and bodies"
        aria-label="Search requests"
        className="h-8 w-full rounded-md border border-line bg-surface-2 pr-9 pl-8 text-sm placeholder:text-faint focus:border-accent focus:bg-surface focus:outline-none"
      />
      <span className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 sm:block">
        <Kbd>/</Kbd>
      </span>
    </form>
  );
}

function LiveIndicator() {
  const { status } = useLive();
  const label = status === "live" ? "Live" : status === "connecting" ? "Connecting" : "Offline";
  const detail =
    status === "live"
      ? "New requests appear as they arrive."
      : status === "connecting"
        ? "Connecting to the live update stream."
        : "Live updates are unavailable. Pages refresh every few seconds instead.";
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted" title={detail}>
      <span
        aria-hidden
        className={cn(
          "inline-block size-2 rounded-full",
          status === "live" && "bg-ok",
          status === "connecting" && "animate-pulse bg-warn",
          status === "offline" && "bg-faint",
        )}
      />
      <span>{label}</span>
      <span className="sr-only">{detail}</span>
    </span>
  );
}
