// Type declarations for mutant.mjs, so a .tsx test file (strict TS,
// noImplicitAny) can statically `import { ... } from './helpers/mutant.mjs'`
// without tripping TS7016. See mutant.mjs for full behavioural documentation.

export const SRC_ROOT: string;

export interface RunWithMutantOptions {
  targetRelPath: string;
  mutate: (original: string) => string;
  childBody: string;
  timeoutMs?: number;
  extraArgs?: string[];
}

export function runWithMutant(opts: RunWithMutantOptions): string;

export interface RunBaselineProbeOptions {
  targetRelPath: string;
  childBody: string;
  timeoutMs?: number;
  extraArgs?: string[];
}

export function runBaselineProbe(opts: RunBaselineProbeOptions): string;

export interface RunMutantSelfReinvokeOptions {
  targetRelPath: string;
  mutate: (original: string) => string;
  testFileAbsPath: string;
  testNamePattern?: string;
  timeoutMs?: number;
}

export interface RunMutantSelfReinvokeResult {
  failed: boolean;
  output: string;
  exitCode: number | null;
  stdout: string;
  stderr: string;
  crashed: boolean;
}

export function runMutantSelfReinvoke(opts: RunMutantSelfReinvokeOptions): RunMutantSelfReinvokeResult;

export interface CreateMutantShadowOptions {
  targetRelPath: string;
  mutate: (original: string) => string;
}

export interface MutantShadow {
  importUrl: (relPath: string) => string;
  cleanup: () => void;
}

export function createMutantShadow(opts: CreateMutantShadowOptions): MutantShadow;
