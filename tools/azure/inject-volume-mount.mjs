#!/usr/bin/env node
// tools/azure/inject-volume-mount.mjs — BUG-716: re-attach the durable
// Azure Files mount to the metroserve Container App without clobbering
// anything else in its spec.
//
// WHY THIS EXISTS (BUG-716): `az containerapp create` on this runner's az
// cli/extension version does not recognize --azure-file-volume-* flags, so
// inc1's deploy landed metroserve on EPHEMERAL in-image /data. The proven
// mechanism for adding a volume mount post-hoc (used live by the lead to
// fix BUG-718's --args defect) is `az containerapp update --yaml <file>`:
// read the app's CURRENT full spec, make the smallest possible edit, write
// it back. This script IS that "smallest possible edit" step, applied
// mechanically so it is reproducible/idempotent in a workflow rather than
// a one-off hand edit.
//
// DELIBERATELY JSON, NOT YAML (no js-yaml/yq/pyyaml dependency to keep
// pinned/updated, matching this repo's tools/azure/smoke.mjs precedent of
// staying dependency-free): `az containerapp show -o json` and JSON is a
// syntactic subset of YAML 1.2, so a plain JSON file is a valid input to
// `az containerapp update --yaml <file>` — no YAML library needed on
// either side of this script. Node's built-in JSON.parse/stringify is the
// entire "parser".
//
// USAGE:
//   node tools/azure/inject-volume-mount.mjs \
//     --in app.json --out app.yaml \
//     --container-name metropolis-metroserve \
//     --volume-name metroserve-data-volume \
//     --storage-name metroserve-data \
//     --mount-path /data
//
// INPUT (--in): the exact output of
//   `az containerapp show --name <app> --resource-group <rg> -o json`
// (the live spec — includes image/env/secrets/resources/ingress/etc.,
// none of which this script touches).
//
// OUTPUT (--out): the same document with, under
// properties.template:
//   volumes: [...existing non-matching volumes, {name, storageType: AzureFile, storageName}]
// and on the NAMED container only:
//   volumeMounts: [...existing non-matching mounts, {volumeName, mountPath}]
//
// IDEMPOTENT: re-running this script against its OWN output (or against a
// live spec that already carries the volume from a prior run) replaces
// the matching-by-name entry in place rather than appending a duplicate —
// proven by test/inject-volume-mount.test.mjs, which runs the injection
// twice on the same input and asserts exactly one volume/mount survives.
//
// Read-only w.r.t. Azure: this script never calls `az` itself. The
// workflow step around it does `show` (read) -> this script (pure
// transform) -> `update --yaml` (the one live-mutating call, left to the
// caller so a dry run can stop after this script).

import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) continue;
    const key = a.slice(2);
    const val = argv[i + 1];
    out[key] = val;
    i++;
  }
  return out;
}

export function injectVolumeMount(spec, opts) {
  const { containerName, volumeName, storageName, mountPath } = opts;
  if (!containerName || !volumeName || !storageName || !mountPath) {
    throw new Error(
      "injectVolumeMount: containerName, volumeName, storageName and mountPath are all required",
    );
  }

  // Deep-clone via JSON round-trip: this script's ONLY output channel is
  // JSON, and cloning this way guarantees the result contains nothing
  // that could not survive a JSON.stringify (no surprises at write time).
  const doc = JSON.parse(JSON.stringify(spec));

  const template = doc?.properties?.template;
  if (!template) {
    throw new Error(
      "injectVolumeMount: input has no properties.template — is this the output of `az containerapp show -o json`?",
    );
  }

  // 1) properties.template.volumes: upsert-by-name, preserve everything
  // else already declared (secret volumes, other file shares, etc.).
  const existingVolumes = Array.isArray(template.volumes) ? template.volumes : [];
  const otherVolumes = existingVolumes.filter((v) => v && v.name !== volumeName);
  template.volumes = [
    ...otherVolumes,
    {
      name: volumeName,
      storageType: "AzureFile",
      storageName,
    },
  ];

  // 2) the named container's volumeMounts: upsert-by-volumeName, preserve
  // image/args/env/secrets/resources and every other container in the
  // template untouched.
  const containers = Array.isArray(template.containers) ? template.containers : [];
  let found = false;
  const newContainers = containers.map((c) => {
    if (!c || c.name !== containerName) return c;
    found = true;
    const existingMounts = Array.isArray(c.volumeMounts) ? c.volumeMounts : [];
    const otherMounts = existingMounts.filter((m) => m && m.volumeName !== volumeName);
    return {
      ...c,
      volumeMounts: [...otherMounts, { volumeName, mountPath }],
    };
  });
  if (!found) {
    throw new Error(
      `injectVolumeMount: no container named "${containerName}" found in properties.template.containers`,
    );
  }
  template.containers = newContainers;

  return doc;
}

// Strip fields `az containerapp show` returns that are read-only /
// server-computed and are not accepted (or are actively rejected) as
// INPUT to `az containerapp update --yaml`. This mirrors the shape the
// lead's live BUG-718 fix already proved works when fed back via --yaml —
// keeping the resource's own `properties.template` edit minimal rather
// than replaying the whole ARM resource verbatim.
const READONLY_TOP_LEVEL = ["systemData"];
const READONLY_PROPERTIES = [
  "provisioningState",
  "runningStatus",
  "latestRevisionName",
  "latestReadyRevisionName",
  "latestRevisionFqdn",
  "customDomainVerificationId",
  "outboundIpAddresses",
  "eventStreamEndpoint",
  "managedEnvironmentId", // duplicate of environmentId on `show` output
];

export function stripReadOnly(spec) {
  const doc = JSON.parse(JSON.stringify(spec));
  for (const k of READONLY_TOP_LEVEL) delete doc[k];
  if (doc.properties) {
    for (const k of READONLY_PROPERTIES) delete doc.properties[k];
  }
  return doc;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const required = ["in", "out", "container-name", "volume-name", "storage-name", "mount-path"];
  const missing = required.filter((k) => !args[k]);
  if (missing.length) {
    console.error(
      `Usage: node tools/azure/inject-volume-mount.mjs --in <file> --out <file> --container-name <name> --volume-name <name> --storage-name <name> --mount-path <path>`,
    );
    console.error(`Missing: ${missing.join(", ")}`);
    process.exit(1);
  }

  const raw = readFileSync(args.in, "utf8");
  let spec;
  try {
    spec = JSON.parse(raw);
  } catch (err) {
    console.error(`Failed to parse ${args.in} as JSON: ${err.message}`);
    process.exit(1);
  }

  const stripped = stripReadOnly(spec);
  const updated = injectVolumeMount(stripped, {
    containerName: args["container-name"],
    volumeName: args["volume-name"],
    storageName: args["storage-name"],
    mountPath: args["mount-path"],
  });

  // Written as JSON (valid YAML) — see file header. Pretty-printed purely
  // for human review in CI logs/artifacts; az does not care about
  // whitespace.
  writeFileSync(args.out, JSON.stringify(updated, null, 2) + "\n", "utf8");
  console.log(
    `wrote ${args.out}: volume "${args["volume-name"]}" (storage "${args["storage-name"]}") mounted at "${args["mount-path"]}" on container "${args["container-name"]}"`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
