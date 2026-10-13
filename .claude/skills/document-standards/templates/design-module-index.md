<!--
Template for a module subdirectory's index, modules/<slug>/README.md. Delete
every comment block before committing.

Use this index for a module whose sub-modules have their own documents. The
subdirectory is named for the module's slug and holds the module's own
document design-<slug>.md plus one design-<slug>-<sub>.md per sub-module.
A module that fits in a single document does not get a subdirectory or this
index.

This index covers every file in the subdirectory but itself, and carries the
sub-module status roll-up. The module's own status does not repeat here:
architecture.md already carries it.
-->

# <Module name>

<What the module is and what it is for.>

## Index

| Document | Content |
|---|---|
| [<module-name>](design-<slug>.md) | the module itself |
| [<sub-module>](design-<slug>-<sub>.md) | <what it covers> |

## Implementation Status

<!--
One row per sub-module document, linking to it. Nothing for the module's
own document -- architecture.md carries that line. The module's own
document and each sub-module document are each authoritative for their own
status; this roll-up only links down.

State the status and nothing more. Status is one of
Implemented / Partially implemented / Not implemented.
-->

| Sub-module | Status |
|---|---|
| [<sub-module>](design-<slug>-<sub>.md) | Implemented |
