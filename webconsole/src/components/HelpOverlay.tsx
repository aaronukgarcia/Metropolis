// HelpOverlay.tsx — FEAT-1972079861: Help overlay modal displaying all keyboard bindings.
//
// Renders EXACTLY from the KEYBINDINGS registry (GR#3 SSOT). Never hardcoded strings.
// Groups bindings by category (tool, speed, layer, camera, help).

import { useEffect } from 'react';
import { KEYBINDINGS } from '../sim/keybindings';
import type { KeyBinding } from '../sim/keybindings';

interface HelpOverlayProps {
  isOpen: boolean;
  onClose: () => void;
}

/**
 * Group bindings by category for display.
 */
function groupByCategory(
  bindings: readonly KeyBinding[]
): Record<string, KeyBinding[]> {
  const groups: Record<string, KeyBinding[]> = {};
  for (const b of bindings) {
    if (!groups[b.category]) groups[b.category] = [];
    groups[b.category].push(b);
  }
  return groups;
}

/**
 * Format a key code for display (e.g., 'ArrowUp' → '↑', ' ' → 'Space').
 */
function formatKeyForDisplay(key: string): string {
  const keyMap: Record<string, string> = {
    ' ': 'Space',
    'ArrowUp': '↑',
    'ArrowDown': '↓',
    'ArrowLeft': '←',
    'ArrowRight': '→',
    '+': '+',
    '=': '=',
    '-': '−',
    '[': '[',
    ']': ']',
    '?': '?',
    'Home': 'Home',
  };
  return keyMap[key] || key;
}

/**
 * Category display name.
 */
function categoryDisplayName(cat: string): string {
  const names: Record<string, string> = {
    tool: 'Tools',
    speed: 'Speed',
    layer: 'Layers',
    camera: 'Camera',
    help: 'Help',
  };
  return names[cat] || cat;
}

export function HelpOverlay({ isOpen, onClose }: HelpOverlayProps) {
  // Esc key closes the overlay
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  // Group bindings by category (excluding uiOnly housekeeping bindings that shouldn't appear)
  const groups = groupByCategory(KEYBINDINGS);
  const categoryOrder = ['tool', 'speed', 'layer', 'camera', 'help'] as const;

  return (
    <div
      className="help-overlay-backdrop"
      onClick={onClose}
      role="presentation"
      aria-modal="true"
    >
      <section
        className="panel help-overlay-panel"
        role="dialog"
        aria-labelledby="help-overlay-title"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="panel-h help-overlay-header">
          <span id="help-overlay-title" className="panel-title">
            Keyboard Controls
          </span>
          <button
            className="btn tiny"
            onClick={onClose}
            aria-label="Close help"
          >
            Close
          </button>
        </header>

        <div className="panel-body help-overlay-body">
          {categoryOrder.map((catKey) => {
            const bindings = groups[catKey];
            if (!bindings || bindings.length === 0) return null;

            return (
              <div key={catKey} className="help-category">
                <h3 className="help-category-title">
                  {categoryDisplayName(catKey)}
                </h3>
                <div className="help-bindings">
                  {bindings.map((binding, idx) => (
                    <div key={idx} className="help-binding-row">
                      <kbd className="help-key">
                        {formatKeyForDisplay(binding.key)}
                      </kbd>
                      <span className="help-label">{binding.label}</span>
                      {binding.description && (
                        <span className="help-description">
                          {binding.description}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>

        <footer className="panel-f help-overlay-footer">
          <p className="help-footer-text">
            Press <kbd>Esc</kbd> or <kbd>?</kbd> to close
          </p>
        </footer>
      </section>
    </div>
  );
}
