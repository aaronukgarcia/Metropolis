import { useState, type ReactElement } from 'react';
import { Panel, TabStrip } from '../Tabs';
import { useSim } from '../../sim/simContext';
import { constructionQueueOf } from './ConstructionQueue';
import {
  FinanceOverviewTab,
  FinanceFlowTab,
  FinanceLedgerTab,
  FinanceTrendTab,
  TaxSettingsTab,
  EarningsTab,
  PoliciesTab,
} from './tabs/financeTabs';
import {
  WellbeingTab,
  HousingTab,
  DemographicsTab,
  EmploymentTab,
  MigrationTab,
} from './tabs/populationTabs';
import { UtilitiesTab, EducationTab, HealthTab, SafetyTab } from './tabs/servicesTabs';
import {
  StructuresTab,
  LinesNetworksTab,
  UnlocksTab,
  SpecialistsTab,
  ReferenceTab,
  ConstructionQueueTab,
} from './tabs/buildZoningTabs';
import { MilestonesTab, DemandForecastTab, RevenueForecastTab } from './tabs/projectionsTabs';
import { AlertsCriticalTab, AlertsWarningTab, AlertsInfoTab } from './tabs/alertsTabs';
import { DebugTab } from './tabs/debugTab';

// LeftDock.tsx — FEAT-2326609720 inc2: the six-group tab-tree replan
// (Aaron Q100059 = A1, plus the 2026-09-02 domain-split amendment for
// Services). AC-1: renders exactly Finance / Services / Population /
// Build & Zoning / Projections / Alerts as first-level tabs, each revealing a
// second row of child tabs (§1's grouping table + the Services domain split).
// Debug stays a separate tab OUTSIDE the six-group selector (§1 row 22 /
// AC-1) — relocated here from RightDock (open question 3, recommendation
// (a)) since RightDock is retired as a docked panel (AC-3). The Debug TAB
// ENTRY is unconditional (parity with the old RightDock's TABS array); only
// the state-mutating cheat BUTTONS inside DebugTab's body stay DEV-gated
// (debugActions(import.meta.env?.DEV), in debugTab.tsx) — see the D1 fix
// note in LeftDock() below.

interface ChildTab {
  id: string;
  label: string;
  Body: () => ReactElement;
}

interface TopGroup {
  id: string;
  label: string;
  children: ChildTab[];
}

const GROUPS: TopGroup[] = [
  {
    id: 'finance',
    label: 'Finance',
    children: [
      { id: 'overview', label: 'Overview', Body: FinanceOverviewTab },
      { id: 'flow', label: 'Flow', Body: FinanceFlowTab },
      { id: 'ledger', label: 'Ledger', Body: FinanceLedgerTab },
      { id: 'trend', label: 'Trend', Body: FinanceTrendTab },
      { id: 'taxSettings', label: 'Tax Settings', Body: TaxSettingsTab },
      { id: 'earnings', label: 'Earnings', Body: EarningsTab },
      { id: 'policies', label: 'Policies', Body: PoliciesTab },
    ],
  },
  {
    id: 'services',
    label: 'Services',
    // Aaron's domain-split amendment (2026-09-02): Utilities / Education /
    // Health / Safety supersede the spec's single "Coverage Map" child.
    children: [
      { id: 'utilities', label: 'Utilities', Body: UtilitiesTab },
      { id: 'education', label: 'Education', Body: EducationTab },
      { id: 'health', label: 'Health', Body: HealthTab },
      { id: 'safety', label: 'Safety', Body: SafetyTab },
    ],
  },
  {
    id: 'population',
    label: 'Population',
    children: [
      { id: 'wellbeing', label: 'Wellbeing', Body: WellbeingTab },
      { id: 'housing', label: 'Housing', Body: HousingTab },
      { id: 'demographics', label: 'Demographics', Body: DemographicsTab },
      { id: 'employment', label: 'Employment', Body: EmploymentTab },
      { id: 'migration', label: 'Migration', Body: MigrationTab },
    ],
  },
  {
    id: 'buildZoning',
    label: 'Build & Zoning',
    children: [
      // BUG-605 (Aaron: "I still don't see the queue") — Queue is FIRST so it
      // is the default child tab shown the moment Build & Zoning is opened,
      // and it always renders (no dev-flag gate, explicit empty state).
      { id: 'queue', label: 'Queue', Body: ConstructionQueueTab },
      { id: 'structures', label: 'Structures', Body: StructuresTab },
      { id: 'lines', label: 'Lines & Networks', Body: LinesNetworksTab },
      { id: 'unlocks', label: 'Unlocks', Body: UnlocksTab },
      { id: 'specialists', label: 'Specialists', Body: SpecialistsTab },
      { id: 'reference', label: 'Reference', Body: ReferenceTab },
    ],
  },
  {
    id: 'projections',
    label: 'Projections',
    children: [
      { id: 'milestones', label: 'Milestones', Body: MilestonesTab },
      { id: 'demand', label: 'Demand', Body: DemandForecastTab },
      { id: 'revenue', label: 'Revenue', Body: RevenueForecastTab },
    ],
  },
  {
    id: 'alerts',
    label: 'Alerts',
    children: [
      { id: 'critical', label: 'Critical', Body: AlertsCriticalTab },
      { id: 'warning', label: 'Warning', Body: AlertsWarningTab },
      { id: 'info', label: 'Info', Body: AlertsInfoTab },
    ],
  },
];

const DEBUG_GROUP_ID = 'debug';

/**
 * BUG-605 (exported so tests can prove the badge logic without needing to
 * simulate clicking into the Build & Zoning group in an SSR render): folds
 * the queue count into the 'queue' child tab's label, "Queue (N)"; every
 * other child id, and a zero count, pass its base label through unchanged.
 */
export function queueChildLabel(childId: string, baseLabel: string, queueCount: number): string {
  return childId === 'queue' && queueCount > 0 ? `${baseLabel} (${queueCount})` : baseLabel;
}

export function LeftDock() {
  const { state } = useSim();
  const [groupId, setGroupId] = useState(GROUPS[0].id);
  const [childByGroup, setChildByGroup] = useState<Record<string, string>>(
    () => Object.fromEntries(GROUPS.map((g) => [g.id, g.children[0].id])),
  );

  const activeGroup = groupId === DEBUG_GROUP_ID ? null : GROUPS.find((g) => g.id === groupId) ?? GROUPS[0];
  // D1 fix (independent round REJECT): the Debug tab ENTRY is unconditional —
  // parity with the old RightDock, which declared its `debug` tab
  // unconditionally in TABS and DEV-gated only the cheat ACTIONS inside
  // DebugTab's body (debugActions(import.meta.env?.DEV), still true in
  // debugTab.tsx unchanged). Gating the tab entry itself hid the entire
  // dogfood bug-reporting surface (Download/Commit/Refresh debug.json, the
  // "Errors captured" MET-code list — a GR#1 pillar) from every non-DEV
  // (production/dogfood `vite build`) run.
  const topTabs = [
    ...GROUPS.map((g) => ({ id: g.id, label: g.label })),
    { id: DEBUG_GROUP_ID, label: 'Debug' },
  ];

  // BUG-605: TabDef (Tabs.tsx) has no separate badge slot, so the Queue count
  // is folded into the child tab's own label — "Queue (N)" — the same low-risk
  // approach Alerts' group (no count badge either) leaves untouched. Read-only:
  // constructionQueueOf is a pure derivation over existing state, no new state.
  const queueCount = constructionQueueOf(state).length;
  const childTabs = activeGroup
    ? activeGroup.children.map((c) => ({ id: c.id, label: queueChildLabel(c.id, c.label, queueCount) }))
    : [];
  const activeChildId = activeGroup ? (childByGroup[activeGroup.id] ?? activeGroup.children[0].id) : null;
  const ActiveBody = activeGroup
    ? activeGroup.children.find((c) => c.id === activeChildId)?.Body ?? activeGroup.children[0].Body
    : DebugTab;

  return (
    <Panel title="City" tabs={topTabs} active={groupId} onSelect={setGroupId}>
      {activeGroup && (
        <TabStrip
          tabs={childTabs}
          active={activeChildId ?? activeGroup.children[0].id}
          onSelect={(id) => setChildByGroup((m) => ({ ...m, [activeGroup.id]: id }))}
        />
      )}
      <div className="dock-tab-content">
        <ActiveBody />
      </div>
    </Panel>
  );
}
