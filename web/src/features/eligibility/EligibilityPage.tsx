import { useState } from "react";
import { PageHeader } from "@/app/page";
import { cn } from "@/lib/cn";
import { SourcesSection } from "./SourcesSection";
import { TierRulesSection } from "./TierRulesSection";

type Tab = "sources" | "rules";

// EligibilityPage merges the former Attestors + Tiers pages (S0008): one pipeline,
// two sections. Sources produce named facts; Tier rules consume the aggregate to
// assign a tier. Keeping them on one page restores the lost upstream/downstream
// context. RBAC = operator (same as the two pages it replaces).
export function EligibilityPage() {
  const [tab, setTab] = useState<Tab>("sources");

  return (
    <>
      <PageHeader
        title="Eligibility"
        description="On-chain credential sources and the tier rules that consume them. Sources produce named facts; tier rules map the aggregate to a tier (first match wins)."
      />

      <div className="mb-4 inline-flex gap-1 rounded-lg border bg-card p-1">
        {(
          [
            ["sources", "Sources"],
            ["rules", "Tier rules"],
          ] as [Tab, string][]
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            className={cn(
              "rounded-md px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary",
              tab === key
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "sources" ? <SourcesSection /> : <TierRulesSection />}
    </>
  );
}
