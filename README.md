# yinc

YAML include (Go implementation)

# usage

```shell
> yinc including.yml
```

## directive

sample

```yaml
a:
  !include child1.yml
b: !include child2.yml
c:
  d:
    !include child3.yml
  # e: !include child4.yml
  f: !include $(shell yq '.child1' child1.yml)
g: !include sub1/child5.yml
h: !include sub2/child6.yml
i: !include $(json json/child7.json)
j: !include https://anywhere/resource
```

- `!include <path>`: include local file. accept relative paths.
- `!include <url>`: include remote resources. accept http and https.
- `!include $(json <path>)`: include local json file. will be converted to YAML before include.
- `!include $(shell <command>)`: include command output.
