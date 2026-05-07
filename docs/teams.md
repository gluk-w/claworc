# Teams

Teams group OpenClaw instances and users. Every Claworc deployment ships
with one seeded **Default** team; admins can create additional teams from
the Instances page or `/teams` admin page.

## Concepts

- **Team** — a named bucket of instances. Each instance belongs to exactly
  one team. Deleting a team is allowed only when it is empty and is not
  the Default team.
- **Team membership** — a user can belong to any number of teams. Each
  membership carries a per-team role.
- **Roles** — `user` or `manager`. Admins are global and effectively
  manage every team.

## Permissions

| Action                          | Admin | Manager (of team) | User (of team)                     |
| ------------------------------- | ----- | ----------------- | ---------------------------------- |
| View team                       | ✓     | ✓                 | ✓                                  |
| List instances of the team      | ✓     | ✓ (all)           | only those granted via UserInstance |
| Create instance in the team     | ✓     | ✓                 | ✗                                  |
| Start / stop / restart instance | ✓     | ✓                 | ✗                                  |
| Delete instance                 | ✓     | ✗                 | ✗                                  |
| Create / delete teams           | ✓     | ✗                 | ✗                                  |
| Manage team members             | ✓     | ✗                 | ✗                                  |
| Manage team provider whitelist  | ✓     | ✗                 | ✗                                  |
| View instance (chat, terminal)  | ✓     | ✓                 | ✓ (if granted via UserInstance)    |

The per-instance assignment (the existing UserInstance grant) is preserved.
Managers bypass it because they implicitly access every instance in their
team. Regular `user`-role members see only the instances explicitly assigned
to them inside teams they belong to.

## Active team selector

The **Instances** page shows a dropdown in its top-left corner with the
caller's teams. The selection is persisted in browser `localStorage`
(`claworc.activeTeamId`) so it survives reloads and tab switches.

For admins, the dropdown also offers a `+ Create a team` action.

## Provider whitelist

A team can be restricted to a subset of the globally configured LLM
providers. The whitelist is set on the team admin page (`/teams` →
Providers tab) and applies when the gateway issues virtual keys to a new
or updated instance:

- **Empty whitelist** — no restriction; the instance can use any global
  provider.
- **Non-empty whitelist** — only the listed global providers are issued
  gateway keys for the team's instances. Instance-specific providers
  (those scoped to a single instance via the instance details page) are
  unaffected by team whitelists.

## LLM Usage page

The Usage page's **Instance** dropdown is grouped by team. Team names
appear as bold headers in the list and are themselves selectable: picking
a team filters usage stats to all instances in that team. Picking an
instance filters to a single instance, the same as before.

## Migrating existing installs

On first run after upgrading, the migration:

1. Creates a Default team if none exists.
2. Backfills `instance.team_id` to the Default team for any existing
   instance.
3. Creates a `TeamMember(role="user")` row for every legacy
   `UserInstance` grant, scoped to the assigned instance's team.
4. Promotes any user with the legacy `can_create_instances=true` flag to
   `manager` of the Default team.

`UserInstance` is preserved so existing per-instance grants continue to
work.
