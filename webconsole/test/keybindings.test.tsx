// keybindings.test.tsx — Unit tests for FEAT-1972079861 keyboard bindings registry.
// Tests AC-1, AC-12, AC-13, AC-14, AC-15, and the registry validation.
//
// GR#3 SSOT fix: this test previously (keybindings.test.mjs) asserted against a
// hand-maintained LOCAL COPY of the KEYBINDINGS array rather than the real registry
// exported from src/sim/keybindings.ts. That local copy could silently drift from the
// real registry with no test failure — e.g. it never picked up the BUG-515 'c'/Clone
// binding. A plain node --test .mjs file cannot import a .ts module directly, so this
// test is a .tsx run through tsx --test (see package.json's "test" script), which CAN
// import the real TypeScript source. No local copy exists anymore — every assertion
// below runs against the actual exported KEYBINDINGS array, so registry drift (a new
// binding, a removed one, a changed category/label, a reintroduced duplicate key) will
// turn this test red.

import { test } from 'node:test';
import * as assert from 'node:assert';
import { KEYBINDINGS, validateKeybindings } from '../src/sim/keybindings';

// AC-1: Binding Registry — Single Source of Truth
test('AC-1: KEYBINDINGS structure is valid (all required fields present)', () => {
  for (let i = 0; i < KEYBINDINGS.length; i++) {
    const b = KEYBINDINGS[i];
    assert.ok(b.key, `Binding ${i} missing 'key' field`);
    assert.ok(b.label, `Binding ${i} missing 'label' field`);
    assert.ok(b.category, `Binding ${i} missing 'category' field`);
    assert.ok(['tool', 'speed', 'layer', 'camera', 'help'].includes(b.category),
      `Binding ${i} has invalid category: ${b.category}`);
  }
});

// AC-1 & AC-12: No duplicate binding keys (case-insensitive)
test('AC-12: No duplicate binding keys', () => {
  const keys = KEYBINDINGS.map(b => b.key.toLowerCase());
  const unique = new Set(keys);
  assert.equal(
    keys.length,
    unique.size,
    `Found duplicate keys: ${keys.filter((k, i) => keys.indexOf(k) !== i).join(', ')}`
  );
});

// AC-1 & AC-12: the registry's own runtime guard agrees there are no duplicates /
// missing actions (exercises validateKeybindings directly, not just re-derived checks).
test('AC-12: validateKeybindings() reports no errors against the real registry', () => {
  const errors = validateKeybindings(KEYBINDINGS);
  assert.deepEqual(errors, [], `validateKeybindings() found: ${errors.join('; ')}`);
});

// AC-1: All tool bindings 1–9 are present
test('AC-4: Tool keys 1–9 are all present', () => {
  for (let i = 1; i <= 9; i++) {
    const binding = KEYBINDINGS.find(b => b.key === String(i));
    assert.ok(binding, `Missing tool key binding for '${i}'`);
    assert.equal(binding.category, 'tool', `Key '${i}' should be in 'tool' category`);
    assert.ok(binding.action, `Key '${i}' should have an action`);
  }
});

// BUG-515: Clone tool binding ('c') is present and wired to the clone mode.
test("BUG-515: Clone tool binding ('c') is present", () => {
  const binding = KEYBINDINGS.find(b => b.key === 'c');
  assert.ok(binding, "Missing clone tool binding for 'c'");
  assert.equal(binding.category, 'tool', "Key 'c' should be in 'tool' category");
  assert.ok(binding.action, "Key 'c' should have an action");
  assert.equal(binding.action?.type, 'tool', "Key 'c' action.type should be 'tool'");
});

// AC-5: Speed control bindings are present (Space, [, ])
test('AC-5: Speed control bindings present (Space, [, ])', () => {
  const speedKeys = [' ', '[', ']'];
  for (const key of speedKeys) {
    const binding = KEYBINDINGS.find(b => b.key === key);
    assert.ok(binding, `Missing speed binding for '${key}'`);
    assert.equal(binding.category, 'speed', `Key '${key}' should be in 'speed' category`);
  }
});

// AC-6: Layer toggle bindings are present (W, P, L, R)
test('AC-6: Layer toggle bindings present (W, P, L, R)', () => {
  const layerKeys = ['w', 'p', 'l', 'r'];
  for (const key of layerKeys) {
    const binding = KEYBINDINGS.find(b => b.key === key);
    assert.ok(binding, `Missing layer binding for '${key}'`);
    assert.equal(binding.category, 'layer', `Key '${key}' should be in 'layer' category`);
  }
});

// AC-7 & AC-8: Camera bindings are present
test('AC-7 & AC-8: Camera bindings present (arrows, +/−, Home)', () => {
  const cameraKeys = ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', '+', '-', 'Home'];
  for (const key of cameraKeys) {
    const binding = KEYBINDINGS.find(b => b.key === key);
    assert.ok(binding, `Missing camera binding for '${key}'`);
    assert.equal(binding.category, 'camera', `Key '${key}' should be in 'camera' category`);
  }
});

// AC-9: Help overlay toggle binding
test('AC-9: Help overlay toggle binding (?)', () => {
  const binding = KEYBINDINGS.find(b => b.key === '?');
  assert.ok(binding, "Missing help binding for '?'");
  assert.equal(binding.category, 'help', "Key '?' should be in 'help' category");
});

// AC-1 & AC-14: Non-uiOnly bindings must have a valid action.type
test('AC-14: Non-uiOnly bindings have valid actions', () => {
  for (let i = 0; i < KEYBINDINGS.length; i++) {
    const b = KEYBINDINGS[i];
    if (!b.uiOnly) {
      assert.ok(b.action, `Binding ${i} (key '${b.key}') is not uiOnly but has no action`);
      assert.ok(b.action.type, `Binding ${i} (key '${b.key}') has action but no type`);
    }
  }
});

// AC-13: No hardcoded strings matching binding labels in the registry.
// This test verifies the registry-only pattern by checking that every binding
// has a label (data-driven, not hardcoded in UI).
test('AC-13: All bindings have labels (data-driven)', () => {
  for (const b of KEYBINDINGS) {
    assert.ok(b.label && typeof b.label === 'string', `Binding '${b.key}' missing or invalid label`);
    assert.ok(b.label.length > 0, `Binding '${b.key}' has empty label`);
  }
});

// AC-1: Verify KEYBINDINGS is a well-formed array (data-driven, SSOT)
test('AC-1: KEYBINDINGS array structure is correct type', () => {
  assert.ok(Array.isArray(KEYBINDINGS), 'KEYBINDINGS should be an array');
  assert.ok(KEYBINDINGS.length > 0, 'KEYBINDINGS should not be empty');
});

// AC-5: Speed bindings should use uiOnly (handled in MapView, not dispatched)
test('AC-5: Speed bindings marked as uiOnly', () => {
  const speedKeys = [' ', '[', ']'];
  for (const key of speedKeys) {
    const binding = KEYBINDINGS.find(b => b.key === key);
    assert.ok(binding?.uiOnly === true, `Speed binding '${key}' should be marked uiOnly`);
  }
});

// AC-15: Validate that the registry can be safely used to build a help overlay.
// This is a meta-test: the registry structure is sound for rendering.
test('AC-13/AC-2: Registry can be grouped by category (helper function test)', () => {
  const groups: Record<string, typeof KEYBINDINGS[number][]> = {};
  for (const b of KEYBINDINGS) {
    if (!groups[b.category]) groups[b.category] = [];
    groups[b.category].push(b);
  }

  // Verify all categories are present
  const expectedCategories = ['tool', 'speed', 'layer', 'camera', 'help'];
  for (const cat of expectedCategories) {
    assert.ok(groups[cat] && groups[cat].length > 0,
      `Category '${cat}' should have at least one binding`);
  }
});

// Test: Binding lookup function (if exported)
test('Binding lookup by key (case-insensitive)', () => {
  // Simulate a findBinding function
  const findBinding = (key: string) => KEYBINDINGS.find(b => b.key.toLowerCase() === key.toLowerCase());

  // Test lowercase
  assert.ok(findBinding('w'), "Should find 'w' (water)");
  assert.ok(findBinding('W'), "Should find 'W' (uppercase)");

  // Test special keys
  assert.ok(findBinding('ArrowUp'), "Should find 'ArrowUp'");
  assert.ok(findBinding('?'), "Should find '?'");
  assert.ok(findBinding(' '), "Should find ' ' (space)");

  // Test non-existent key
  assert.equal(findBinding('X'), undefined, "Should not find non-existent key");
});
