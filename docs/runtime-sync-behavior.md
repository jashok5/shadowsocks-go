# Runtime Sync Behavior

## Port Offset Priority

When `node.get_port_offset_by_node_name` is enabled:

1. The service pulls `nodes` list and finds current node by `node.id`.
2. If node `name`/`node_name` contains `#<offset>` (for example `HK #9900`), that offset is used.
3. If parsing fails or pattern does not exist, fallback to API `nodes/{id}/info` field `port_offset`.

Priority summary:

- parsed offset from node name `#<offset>` (when available)
- fallback `nodeinfo.port_offset`

## Examples

| node name / node_name | nodeinfo.port_offset | parsed result | effective offset | reason |
| --- | ---: | ---: | ---: | --- |
| `HK #9900` | `0` | `9900` | `9900` | name pattern matched |
| `SG-Edge #1200` | `300` | `1200` | `1200` | name pattern matched |
| `JP-Tokyo` | `700` | N/A | `700` | no `#<offset>` in name |
| `US #abc` | `800` | N/A | `800` | invalid numeric part |
| `node_name = KR #4500` | `200` | `4500` | `4500` | fallback to `node_name` field |

## Log Samples

When name-based offset is parsed and applied:

```text
INFO  port offset resolved from node name
      node_id=5 node_name="HK #9900" old=0 new=9900
```

When `nodes` pull fails (keeps fallback behavior):

```text
WARN  pull nodes for port offset by node name failed
      error="..."
```

Practical check points:

- if you see `port offset resolved from node name`, effective offset is `new`
- if this log is absent, runtime uses `nodeinfo.port_offset`

## Notes

- This behavior is for compatibility with legacy Python deployment style.
- It is safe to disable by setting `node.get_port_offset_by_node_name: false`.
