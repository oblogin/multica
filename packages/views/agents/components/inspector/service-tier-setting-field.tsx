"use client";

import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, Gauge } from "lucide-react";
import type { RuntimeModelServiceTier } from "@multica/core/types";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import {
  PickerItem,
  PropertyPicker,
} from "../../../issues/components/pickers";
import { SettingsRow } from "../../../settings/components/settings-layout";
import { useT } from "../../../i18n";
import { findModelCapabilityEntry } from "./model-capability";

/**
 * Full-width service-tier field for Codex agents. Capability comes from the
 * resolved model's live catalog rather than a hard-coded Fast switch. When the
 * model follows config.toml, Fast is offered if the CLI advertises it for any
 * model; Codex resolves its configured model when the agent runs.
 */
export function ServiceTierSettingField({
  label,
  runtimeId,
  runtimeOnline,
  provider,
  model,
  value,
  canEdit,
  onChange,
}: {
  label: ReactNode;
  runtimeId: string | null;
  runtimeOnline: boolean;
  provider: string;
  model: string;
  value: string;
  canEdit: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? runtimeId : null),
  );
  const models = modelsQuery.data?.models ?? [];
  const entry = findModelCapabilityEntry(models, model, provider);
  const defaultModelFastTier =
    provider === "codex" && !model
      ? models.flatMap((candidate) => candidate.service_tiers ?? []).find(
          (tier) => tier.id === "priority",
        )
      : undefined;
  const tiers = entry?.service_tiers ?? (defaultModelFastTier ? [defaultModelFastTier] : []);
  const supportsExplicitStandard = models.some(
    (candidate) =>
      candidate.supports_explicit_standard_service_tier === true,
  );

  if (tiers.length === 0 && !supportsExplicitStandard && !value) return null;

  return (
    <SettingsRow label={label} size="select-wide">
      <ServiceTierPicker
        value={value}
        tiers={tiers}
        supportsExplicitStandard={supportsExplicitStandard}
        canEdit={canEdit}
        onChange={onChange}
      />
    </SettingsRow>
  );
}

function ServiceTierPicker({
  value,
  tiers,
  supportsExplicitStandard,
  canEdit,
  onChange,
}: {
  value: string;
  tiers: RuntimeModelServiceTier[];
  supportsExplicitStandard: boolean;
  canEdit: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const availableTiers = supportsExplicitStandard
    ? [
          {
            id: "default",
            name: t(($) => $.pickers.service_tier_standard),
            description: t(
              ($) => $.pickers.service_tier_standard_description,
            ),
          },
          ...tiers.filter((tier) => tier.id !== "default"),
      ]
    : tiers;
  const selected = value
    ? availableTiers.find((tier) => tier.id === value)
    : undefined;
  const triggerLabel =
    selected?.name || value || t(($) => $.pickers.service_tier_default);
  const triggerTitle = t(($) => $.pickers.service_tier_tooltip, {
    value: triggerLabel,
  });

  const select = async (next: string) => {
    setOpen(false);
    if (next !== value) await onChange(next);
  };

  const display = (
    <div className="flex min-h-10 items-center gap-2 rounded-lg border border-input bg-input/50 px-3 text-body text-muted-foreground">
      <Gauge className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate">{triggerLabel}</span>
    </div>
  );
  if (!canEdit) return display;

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-[var(--anchor-width)] min-w-[14rem] max-w-md"
      align="start"
      tooltip={triggerTitle}
      triggerRender={
        <button
          type="button"
          className="flex min-h-10 w-full min-w-0 items-center gap-2 rounded-lg border border-input bg-transparent px-3 text-left text-body transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          aria-label={triggerTitle}
        />
      }
      trigger={
        <>
          <Gauge
            className="h-4 w-4 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1 truncate">{triggerLabel}</span>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${
              open ? "rotate-180" : ""
            }`}
            aria-hidden="true"
          />
        </>
      }
    >
      <PickerItem
        selected={value === ""}
        emptyValue
        onClick={() => void select("")}
      >
        <span className="block min-w-0 flex-1 text-left">
          <span className="truncate text-label font-medium">
            {t(($) => $.pickers.service_tier_default)}
          </span>
          <span className="mt-0.5 block text-micro leading-snug text-muted-foreground">
            {t(($) => $.pickers.service_tier_default_description)}
          </span>
        </span>
      </PickerItem>
      {availableTiers.map((tier) => (
        <PickerItem
          key={tier.id}
          selected={tier.id === value}
          onClick={() => void select(tier.id)}
        >
          <span className="block min-w-0 flex-1 text-left">
            <span className="truncate text-label font-medium">
              {tier.name}
            </span>
            {tier.description ? (
              <span className="mt-0.5 block text-micro leading-snug text-muted-foreground">
                {tier.description}
              </span>
            ) : null}
          </span>
        </PickerItem>
      ))}
    </PropertyPicker>
  );
}
