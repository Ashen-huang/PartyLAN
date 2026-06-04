# PartyLAN Tailnet Policy DERP Merge Examples

## Purpose

Provide copy-paste-safe examples for merging the custom PartyLAN DERP region
into real-world Tailscale tailnet policy files that already contain ACLs,
Grants, SSH rules, and policy tests.

These examples assume the DERP server is already deployed and the remaining
work is to save a correct `derpMap` into the active tailnet policy so clients
can receive region `901/cn1`.

## Fixed Deployment Values

These fields must stay exactly aligned with the current server deployment:

- `regionID`: `901`
- `regionCode`: `"cn1"`
- `regionName`: `"PartyLAN CN Relay"`
- `name`: `"901a"`
- `hostName`: `"103.236.92.32"`
- `derpPort`: `443`
- `stunPort`: `3479`
- `ipv4`: `"103.236.92.32"`
- `certName`: `"sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"`

## Placement Rule

`derpMap` is a top-level policy key. It sits alongside keys such as `acls`,
`grants`, `ssh`, `tests`, and `sshTests`. Do not nest `derpMap` inside any of
those sections.

If the tailnet policy already contains a top-level `derpMap`, merge only
`regions["901"]` into the existing `regions` map. Do not create a second
top-level `derpMap` block.

Keep default public DERP regions enabled during first rollout validation. Do
not add `omitDefaultRegions` yet.

## Canonical DERP Region Block

Use this region object whenever a template below says "merge `regions[901]`":

```jsonc
"901": {
  "regionID": 901,
  "regionCode": "cn1",
  "regionName": "PartyLAN CN Relay",
  "nodes": [
    {
      "name": "901a",
      "regionID": 901,
      "hostName": "103.236.92.32",
      "derpPort": 443,
      "stunPort": 3479,
      "ipv4": "103.236.92.32",
      "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
    }
  ]
}
```

## Template 1: ACL + SSH + tests

### Full Policy Example

```jsonc
{
  "tagOwners": {
    "tag:server": ["autogroup:admin"]
  },
  "acls": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["*:*"]
    }
  ],
  "ssh": [
    {
      "action": "check",
      "src": ["autogroup:member"],
      "dst": ["tag:server"],
      "users": ["root", "ubuntu"]
    }
  ],
  "tests": [
    {
      "src": "autogroup:member",
      "accept": ["tag:server:22"]
    }
  ],
  "derpMap": {
    "regions": {
      "901": {
        "regionID": 901,
        "regionCode": "cn1",
        "regionName": "PartyLAN CN Relay",
        "nodes": [
          {
            "name": "901a",
            "regionID": 901,
            "hostName": "103.236.92.32",
            "derpPort": 443,
            "stunPort": 3479,
            "ipv4": "103.236.92.32",
            "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
          }
        ]
      }
    }
  }
}
```

### Merge-Only Version

If the existing policy already has `acls`, `ssh`, and `tests`, add only this
top-level block if `derpMap` does not exist yet:

```jsonc
"derpMap": {
  "regions": {
    "901": {
      "regionID": 901,
      "regionCode": "cn1",
      "regionName": "PartyLAN CN Relay",
      "nodes": [
        {
          "name": "901a",
          "regionID": 901,
          "hostName": "103.236.92.32",
          "derpPort": 443,
          "stunPort": 3479,
          "ipv4": "103.236.92.32",
          "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
        }
      ]
    }
  }
}
```

If `derpMap` already exists, merge only this into `derpMap.regions`:

```jsonc
"901": {
  "regionID": 901,
  "regionCode": "cn1",
  "regionName": "PartyLAN CN Relay",
  "nodes": [
    {
      "name": "901a",
      "regionID": 901,
      "hostName": "103.236.92.32",
      "derpPort": 443,
      "stunPort": 3479,
      "ipv4": "103.236.92.32",
      "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
    }
  ]
}
```

## Template 2: Grants + SSH + sshTests

### Full Policy Example

```jsonc
{
  "groups": {
    "group:ops": ["alice@example.com", "bob@example.com"]
  },
  "grants": [
    {
      "src": ["group:ops"],
      "dst": ["tag:server"],
      "ip": ["*"]
    }
  ],
  "ssh": [
    {
      "action": "accept",
      "src": ["group:ops"],
      "dst": ["tag:server"],
      "users": ["root", "ubuntu"]
    }
  ],
  "sshTests": [
    {
      "src": "alice@example.com",
      "dst": ["tag:server"],
      "accept": ["root", "ubuntu"]
    }
  ],
  "derpMap": {
    "regions": {
      "901": {
        "regionID": 901,
        "regionCode": "cn1",
        "regionName": "PartyLAN CN Relay",
        "nodes": [
          {
            "name": "901a",
            "regionID": 901,
            "hostName": "103.236.92.32",
            "derpPort": 443,
            "stunPort": 3479,
            "ipv4": "103.236.92.32",
            "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
          }
        ]
      }
    }
  }
}
```

### Merge-Only Version

If the policy already uses `grants`, `ssh`, and `sshTests`, add only this
top-level block if `derpMap` does not exist:

```jsonc
"derpMap": {
  "regions": {
    "901": {
      "regionID": 901,
      "regionCode": "cn1",
      "regionName": "PartyLAN CN Relay",
      "nodes": [
        {
          "name": "901a",
          "regionID": 901,
          "hostName": "103.236.92.32",
          "derpPort": 443,
          "stunPort": 3479,
          "ipv4": "103.236.92.32",
          "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
        }
      ]
    }
  }
}
```

If `derpMap` already exists, merge only `regions["901"]` using the canonical
block above.

## Template 3: ACL + Grants + SSH + tests + sshTests

### Full Policy Example

```jsonc
{
  "groups": {
    "group:ops": ["alice@example.com", "bob@example.com"]
  },
  "tagOwners": {
    "tag:server": ["autogroup:admin"]
  },
  "acls": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["*:*"]
    }
  ],
  "grants": [
    {
      "src": ["group:ops"],
      "dst": ["tag:server"],
      "ip": ["*"]
    }
  ],
  "ssh": [
    {
      "action": "check",
      "src": ["group:ops"],
      "dst": ["tag:server"],
      "users": ["root", "ubuntu"]
    }
  ],
  "tests": [
    {
      "src": "autogroup:member",
      "accept": ["tag:server:22"]
    }
  ],
  "sshTests": [
    {
      "src": "alice@example.com",
      "dst": ["tag:server"],
      "accept": ["root", "ubuntu"]
    }
  ],
  "derpMap": {
    "regions": {
      "901": {
        "regionID": 901,
        "regionCode": "cn1",
        "regionName": "PartyLAN CN Relay",
        "nodes": [
          {
            "name": "901a",
            "regionID": 901,
            "hostName": "103.236.92.32",
            "derpPort": 443,
            "stunPort": 3479,
            "ipv4": "103.236.92.32",
            "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
          }
        ]
      }
    }
  }
}
```

### Merge-Only Version

If the policy already has all of `acls`, `grants`, `ssh`, `tests`, and
`sshTests`, then:

- add a top-level `derpMap` block if none exists yet, or
- merge only `regions["901"]` if a top-level `derpMap` already exists

Top-level `derpMap` add:

```jsonc
"derpMap": {
  "regions": {
    "901": {
      "regionID": 901,
      "regionCode": "cn1",
      "regionName": "PartyLAN CN Relay",
      "nodes": [
        {
          "name": "901a",
          "regionID": 901,
          "hostName": "103.236.92.32",
          "derpPort": 443,
          "stunPort": 3479,
          "ipv4": "103.236.92.32",
          "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
        }
      ]
    }
  }
}
```

Existing `derpMap.regions` merge:

```jsonc
"901": {
  "regionID": 901,
  "regionCode": "cn1",
  "regionName": "PartyLAN CN Relay",
  "nodes": [
    {
      "name": "901a",
      "regionID": 901,
      "hostName": "103.236.92.32",
      "derpPort": 443,
      "stunPort": 3479,
      "ipv4": "103.236.92.32",
      "certName": "sha256-raw:16593ac9178dd0be94066b1d077a80fafac9fa65d041bc152a906c9cd50c1315"
    }
  ]
}
```

## Common Merge Rules

- Do not put `derpMap` inside `grants`, `acls`, `ssh`, `tests`, or `sshTests`.
- Do not duplicate `derpMap` at top level.
- Do not change `stunPort` back to `3478`; this host uses `3479` because
  `3478/UDP` is already occupied by Screego.
- Do not remove `certName`; the current deployment depends on the self-signed
  certificate pin.
- Do not remove existing unrelated ACL, Grants, SSH, test, DNS, or tag policy
  content while inserting the DERP block.
- Do not enable `omitDefaultRegions` during first rollout validation.

## Save and Validate

1. Open the tailnet policy JSON editor.
2. Merge the correct `derpMap` form for the current policy structure.
3. Save the policy and wait for propagation.
4. On a client, run `tailscale netcheck` and confirm region `901/cn1` appears.
5. Optionally run `tailscale status` and `tailscale ping <peer>` to inspect
   whether the path is direct or relayed.
6. Start PartyLAN with:

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -Mode networked `
  -DERPRegionID 901 `
  -DERPRegionCode "cn1" `
  -DERPHostName "103.236.92.32" `
  -DERPPort 443 `
  -UseNativeMedia `
  -UseNativeCaptureExperiment `
  -UseNativeEncoderExperiment
```

7. Confirm PartyLAN logs change from:
   - `custom DERP missing`
   to:
   - `custom DERP present`

## References

- Tailscale DERP servers: https://tailscale.com/docs/reference/derp-servers
- Tailscale policy syntax: https://tailscale.com/kb/1337/policy-syntax
- Tailscale grants syntax: https://tailscale.com/docs/reference/syntax/grants
