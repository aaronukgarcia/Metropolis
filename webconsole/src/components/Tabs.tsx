import type { ReactNode } from 'react';

export interface TabDef {
  id: string;
  label: string;
}

interface Props {
  tabs: TabDef[];
  active: string;
  onSelect: (id: string) => void;
}

export function TabStrip({ tabs, active, onSelect }: Props) {
  return (
    <div className="tabstrip" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          aria-selected={active === t.id}
          className={`tab${active === t.id ? ' active' : ''}`}
          onClick={() => onSelect(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

export function Panel({
  title,
  tabs,
  active,
  onSelect,
  children,
}: {
  title: string;
  tabs?: TabDef[];
  active?: string;
  onSelect?: (id: string) => void;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      <header className="panel-h">
        <span className="panel-title">{title}</span>
        {tabs && active && onSelect && <TabStrip tabs={tabs} active={active} onSelect={onSelect} />}
      </header>
      <div className="panel-body">{children}</div>
    </section>
  );
}
