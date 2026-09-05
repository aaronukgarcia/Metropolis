// tools/azure/inject-volume-mount.test.mjs — BUG-716: proves the
// Azure Files volume-mount injection is (a) idempotent (re-running never
// duplicates the volume/mount) and (b) non-destructive (image/args/env/
// secrets/resources/other containers survive untouched), against a REAL
// captured `az containerapp show -o json` fixture from the live
// metropolis-metroserve app (2026-09-05), not a hand-typed stand-in.
import { test } from "node:test";
import assert from "node:assert/strict";
import { injectVolumeMount, stripReadOnly } from "./inject-volume-mount.mjs";

// Captured live via `az containerapp show --name metropolis-metroserve
// --resource-group rg-metropolis-dev -o json` on 2026-09-05, trimmed of
// the (very long) outboundIpAddresses list — irrelevant to this script
// and not needed to prove the injection logic.
const LIVE_FIXTURE = {
  id: "/subscriptions/xxx/resourceGroups/rg-metropolis-dev/providers/Microsoft.App/containerapps/metropolis-metroserve",
  identity: { type: "None" },
  location: "UK South",
  name: "metropolis-metroserve",
  properties: {
    configuration: {
      activeRevisionsMode: "Single",
      ingress: {
        external: true,
        fqdn: "metropolis-metroserve.redcliff-2fdfd00d.uksouth.azurecontainerapps.io",
        targetPort: 8080,
        transport: "Auto",
      },
      registries: [
        {
          identity: "",
          passwordSecretRef: "metropolisacrazurecrio-metropolisacr",
          server: "metropolisacr.azurecr.io",
          username: "metropolisacr",
        },
      ],
      secrets: [
        { name: "metropolisacrazurecrio-metropolisacr" },
        { name: "metroserve-shared-secret" },
      ],
    },
    provisioningState: "Succeeded",
    runningStatus: "Running",
    latestRevisionName: "metropolis-metroserve--0000003",
    latestReadyRevisionName: "metropolis-metroserve--0000003",
    latestRevisionFqdn: "metropolis-metroserve--0000003.redcliff-2fdfd00d.uksouth.azurecontainerapps.io",
    customDomainVerificationId: "REDACTED-VERIFICATION-ID",
    environmentId: "/subscriptions/xxx/resourceGroups/rg-metropolis-dev/providers/Microsoft.App/managedEnvironments/metropolis-env",
    managedEnvironmentId: "/subscriptions/xxx/resourceGroups/rg-metropolis-dev/providers/Microsoft.App/managedEnvironments/metropolis-env",
    outboundIpAddresses: ["20.90.238.99", "20.90.239.180"],
    eventStreamEndpoint: "https://uksouth.azurecontainerapps.dev/.../eventstream",
    template: {
      containers: [
        {
          args: [
            "-addr",
            "0.0.0.0:8080",
            "-persist-dir",
            "/data",
            "-city",
            "dogfood",
            "-tick-interval",
            "1s",
            "-snapshot-every",
            "360",
          ],
          env: [{ name: "METROSERVE_SHARED_SECRET", secretRef: "metroserve-shared-secret" }],
          image: "metropolisacr.azurecr.io/metropolis-metroserve:v0.3.0-471-ga3daae9",
          name: "metropolis-metroserve",
          resources: { cpu: 0.5, ephemeralStorage: "2Gi", memory: "1Gi" },
        },
      ],
      initContainers: null,
      revisionSuffix: "",
      scale: {
        cooldownPeriod: 300,
        maxReplicas: 1,
        minReplicas: 0,
        pollingInterval: 30,
        rules: null,
      },
      serviceBinds: null,
      terminationGracePeriodSeconds: null,
      volumes: null,
    },
    workloadProfileName: "Consumption",
  },
  resourceGroup: "rg-metropolis-dev",
  systemData: {
    createdAt: "2026-09-05T05:45:31.7158395",
    createdBy: "Aaron@garcia.ltd",
  },
  type: "Microsoft.App/containerApps",
};

const OPTS = {
  containerName: "metropolis-metroserve",
  volumeName: "metroserve-data-volume",
  storageName: "metroserve-data",
  mountPath: "/data",
};

test("injects volume + volumeMount into a fresh (no-volume) live spec", () => {
  const result = injectVolumeMount(LIVE_FIXTURE, OPTS);

  assert.deepEqual(result.properties.template.volumes, [
    { name: "metroserve-data-volume", storageType: "AzureFile", storageName: "metroserve-data" },
  ]);

  const container = result.properties.template.containers.find(
    (c) => c.name === "metropolis-metroserve",
  );
  assert.deepEqual(container.volumeMounts, [
    { volumeName: "metroserve-data-volume", mountPath: "/data" },
  ]);
});

test("does not clobber image/args/env/resources/other containers", () => {
  const result = injectVolumeMount(LIVE_FIXTURE, OPTS);
  const container = result.properties.template.containers.find(
    (c) => c.name === "metropolis-metroserve",
  );

  assert.equal(container.image, "metropolisacr.azurecr.io/metropolis-metroserve:v0.3.0-471-ga3daae9");
  assert.deepEqual(container.args, [
    "-addr",
    "0.0.0.0:8080",
    "-persist-dir",
    "/data",
    "-city",
    "dogfood",
    "-tick-interval",
    "1s",
    "-snapshot-every",
    "360",
  ]);
  assert.deepEqual(container.env, [
    { name: "METROSERVE_SHARED_SECRET", secretRef: "metroserve-shared-secret" },
  ]);
  assert.deepEqual(container.resources, { cpu: 0.5, ephemeralStorage: "2Gi", memory: "1Gi" });

  // Configuration (ingress/registries/secrets) untouched.
  assert.deepEqual(result.properties.configuration, LIVE_FIXTURE.properties.configuration);
});

test("IDEMPOTENT: running injection twice never duplicates the volume or mount", () => {
  const once = injectVolumeMount(LIVE_FIXTURE, OPTS);
  const twice = injectVolumeMount(once, OPTS);

  assert.equal(twice.properties.template.volumes.length, 1);
  const container = twice.properties.template.containers.find(
    (c) => c.name === "metropolis-metroserve",
  );
  assert.equal(container.volumeMounts.length, 1);
  assert.deepEqual(twice, once, "second injection must be a no-op byte-for-byte (JSON-comparable)");
});

test("preserves a pre-existing, differently-named volume/mount (never wipes other volumes)", () => {
  const withOther = JSON.parse(JSON.stringify(LIVE_FIXTURE));
  withOther.properties.template.volumes = [
    { name: "some-other-volume", storageType: "AzureFile", storageName: "some-other-share" },
  ];
  withOther.properties.template.containers[0].volumeMounts = [
    { volumeName: "some-other-volume", mountPath: "/other" },
  ];

  const result = injectVolumeMount(withOther, OPTS);

  assert.equal(result.properties.template.volumes.length, 2);
  assert.ok(
    result.properties.template.volumes.some((v) => v.name === "some-other-volume"),
    "pre-existing volume must survive",
  );
  assert.ok(
    result.properties.template.volumes.some((v) => v.name === "metroserve-data-volume"),
    "new volume must be added",
  );

  const container = result.properties.template.containers.find(
    (c) => c.name === "metropolis-metroserve",
  );
  assert.equal(container.volumeMounts.length, 2);
});

test("throws a clear error when the named container does not exist", () => {
  assert.throws(
    () => injectVolumeMount(LIVE_FIXTURE, { ...OPTS, containerName: "not-a-real-container" }),
    /no container named/,
  );
});

test("throws a clear error when required options are missing", () => {
  assert.throws(() => injectVolumeMount(LIVE_FIXTURE, { ...OPTS, mountPath: undefined }));
});

test("stripReadOnly removes server-computed fields az update --yaml would choke on / that would be stale", () => {
  const stripped = stripReadOnly(LIVE_FIXTURE);
  assert.equal(stripped.systemData, undefined);
  assert.equal(stripped.properties.provisioningState, undefined);
  assert.equal(stripped.properties.runningStatus, undefined);
  assert.equal(stripped.properties.latestRevisionName, undefined);
  assert.equal(stripped.properties.outboundIpAddresses, undefined);
  // Everything load-bearing survives.
  assert.equal(stripped.properties.template.containers[0].image, LIVE_FIXTURE.properties.template.containers[0].image);
  assert.equal(stripped.properties.configuration.ingress.fqdn, LIVE_FIXTURE.properties.configuration.ingress.fqdn);
});

test("output of injectVolumeMount round-trips through JSON.stringify/parse unchanged (valid as az --yaml input, since JSON is valid YAML)", () => {
  const result = injectVolumeMount(stripReadOnly(LIVE_FIXTURE), OPTS);
  const roundTripped = JSON.parse(JSON.stringify(result));
  assert.deepEqual(roundTripped, result);
});
