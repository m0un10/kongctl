# kongctl

One static binary that composes Kong DB-less declarative config from plain
decK files, substitutes or templates `${NAME}` placeholders, validates and
lints the result, and wraps it in the Kubernetes object that delivers it.
The same binary runs locally, in a git hook, in CI, and as an Argo CD Config
Management Plugin (CMP), so what you test on your laptop is what the cluster
syncs.

It replaces a set of bash scripts that shelled out to `deck`, `yq`, `jq`,
`curl` and `git`. decK's merge, patch, validate and lint run in-process
through Kong's own Go libraries, so behaviour matches `deck file` exactly.

## Commands

```
kongctl compose <env>          merged config, placeholders intact
kongctl placeholders <env>     the ${NAME} values an environment needs
kongctl render <env>           placeholders substituted from the environment
kongctl manifest <env>         Secret | ConfigMap | ExternalSecret carrying the config
kongctl check [env...]         compose + validate + lint, fake secrets, every env
kongctl check --staged         the same for what is staged in git (pre-commit)
kongctl diff                   composed semantic diff of every env vs a git base
kongctl notate [--post]        merge-request note built from the diff
kongctl normalise [file...]    rewrite sources in decK's canonical form
kongctl cmp init|generate      Argo CD CMP entry points
kongctl hook install           install the pre-commit hook
kongctl version
```

stdout carries only the artifact a command produces. Everything else goes
to stderr. Exit codes: 0 ok, 1 a problem with the config, 2 usage or tooling.

## The pipeline

```
<sourceDir>/*.yaml   (+ optional commonDir first, patchesDir after)
      |  merge, exactly as deck file merge       -> compose
      v
composed config with ${NAME} placeholders
      |  substitute (Secret, ConfigMap)  or  template (ExternalSecret)
      v
rendered config      -> deck-equivalent validate, then lint with your ruleset
      |
      v
Secret / ConfigMap / ExternalSecret manifest
```

Placeholders are substituted by literal text replacement, never by a shell
or regex expansion. A malformed placeholder (`${host}`, `${ A }`, unclosed)
fails the run, and so does an unset or empty value, all names reported at
once. `check` and `--fake-secrets` fill unset names with `fake-<NAME>` so the
config can still be validated without any secret access.

### ExternalSecret mode

With `manifest.kind: ExternalSecret`, kongctl never needs a secret value.
The composed config goes into an External Secrets Operator template with
each whole-value placeholder rewritten as `{{ .NAME | quote }}`, and one
`spec.data` entry per placeholder names the store key it comes from. ESO
renders the real Secret in the cluster. Because the manifest is plain text,
Argo CD shows the config in its diff, and the repo-server holds nothing
sensitive. Validation and lint still run on a fake-rendered copy first.

The store reference has no default. Set `externalSecret.storeKind` and
`storeName` in `kongctl.yaml`, or keep a `secret-store.yaml` at the repo
root in this shape, which kongctl reads for drop-in compatibility:

```yaml
secretStoreRef:
  kind: ClusterSecretStore
  name: my-store
refreshInterval: 1h0m0s
keyPrefix: DECK_               # store key = keyPrefix + NAME
keys:                          # names that do not follow the prefix
  MY_TOKEN: MY_TOKEN
```

## Configuration

Precedence: flags > `KONGCTL_*` environment variables > `kongctl.yaml` at
the repo root > defaults. See `kongctl.example.yaml` for every key with its
default. The defaults are conventions that suit any GitOps repository:

| Setting | Default |
|---|---|
| `layout.sourceDir` | `environments/{env}/config/kong` |
| `layout.globs` | `*.yml`, `*.yaml`, non-recursive, merged in name order |
| `layout.commonDir`, `layout.patchesDir` | off |
| `placeholders.mode` | `embedded` (`whole` restores the strict rule) |
| `manifest.kind` / `name` / `key` | `Secret` / `kong-declarative-config` / `kong.yml` |
| `lint.ruleset` / `failSeverity` | `lint/ruleset.yaml` / `error` |
| `diff.baseRef` | `origin/main`, compared at the merge-base |
| `cmp.prefix` | `ARGOCD_ENV_` |

Things that are organisation-specific have no default and must be set:
the environment list (discovered from the layout otherwise), extra
annotations, the ESO store, and the store key prefix.

Legacy variable names from the bash tooling still work: `KONG_CONFIG_ROOT`,
`SECRET_NAME`, `SECRET_KEY`, `FAKE_SECRETS`, `SKIP_VALIDATE`, `SKIP_LINT`,
`ALLOW_LINT_FAILURE`, `RULESET`, `BASE_REF`, `MAX_LINES`, `ENVS`,
`GITLAB_TOKEN` and the `CI_*` variables.

## Argo CD CMP

The image is `scratch` plus the binary and `plugin.yaml`, uid 999, no shell.
Register it as a repo-server sidecar:

```yaml
repoServer:
  extraContainers:
    - name: kong-deck-cmp
      image: ghcr.io/m0un10/kongctl:<version>
      command: [/var/run/argocd/argocd-cmp-server]
      securityContext: {runAsNonRoot: true, runAsUser: 999, readOnlyRootFilesystem: true}
      volumeMounts:
        - {name: var-files, mountPath: /var/run/argocd}
        - {name: plugins, mountPath: /home/argocd/cmp-server/plugins}
        - {name: cmp-tmp, mountPath: /tmp}
      # Secret and ConfigMap kinds only: values arrive as plain variables.
      # envFrom: [{secretRef: {name: kong-secrets}}]
  volumes:
    - {name: cmp-tmp, emptyDir: {}}
```

The plugin registers as `kong-deck-v2`. An Application source:

```yaml
- repoURL: https://git.example.com/platform/kong-config.git
  targetRevision: main
  path: .
  plugin:
    name: kong-deck-v2
    env:
      - {name: ENV_NAME, value: dev}
      - {name: KIND, value: ExternalSecret}     # optional, else kongctl.yaml
      - {name: SECRET_NAME, value: kong-declarative-config}
```

Parameters map onto `kongctl.yaml` keys (`BASEPATH`, `SECRET_KEY`,
`CONFIG_FILE`, `SKIP_VALIDATE`, `SKIP_LINT`, `ALLOW_LINT_FAILURE`). Fake
secrets are only honoured as the bare `FAKE_SECRETS` container variable; a
prefixed copy from an Application is ignored with a warning. Test the image
locally with `make cmp-test ENV=dev`, which runs it against `testdata/repo`
the way the repo-server would.

## Local use

```bash
make build                                   # bin/kongctl
kongctl check                                # every environment, fake secrets
kongctl manifest dev --kind ExternalSecret   # what the CMP would emit
kongctl render dev --env-file dev.secrets.env
kongctl diff --both                          # vs origin/main
kongctl hook install                         # pre-commit = check --staged
```

`kongctl diff` composes both sides before comparing, so splitting a file
into several or reordering entities shows "no semantic change" while a real
change shows once. `kongctl notate --post` puts the same result on the merge
request as a single note that is updated in place on every pipeline run.

## Migrating from the bash tooling

Semantics are the same; a few byte-level details differ:

- Rendered output uses decK's list style (`- item` unindented) rather than
  yq's. A Secret's base64 changes once at cutover; the decoded content is
  equivalent.
- The semantic diff normalises quoting as well as key order.
- `--env-file` is a dotenv reader (`NAME=value`, `export` tolerated, quotes
  stripped); it is not sourced by a shell.
- Numbers pass through `encoding/json`, so integers beyond 2^53 lose
  precision, exactly as in decK.
- A literal `{{` inside the Kong config is escaped in ExternalSecret mode
  rather than rejected.

## Development

```bash
make test        # unit tests, plus one integration test that needs git
make vet
make image       # docker build
```

The merge algorithm and the lint wrapper are ports of Kong's go-apiops and
decK code (Apache-2.0); see NOTICE.
