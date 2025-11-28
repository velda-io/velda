# Weekly Release Pipeline - Usage Guide

This guide explains how to use the automated weekly release pipeline for your open-source project.

## Overview

The weekly release pipeline automatically creates releases every Monday at 9:00 AM UTC. It includes:
- ✅ Automated scheduling (weekly)
- ✅ Change detection (skips if no commits)
- ✅ Auto-generated changelog
- ✅ GitHub releases with notes
- ✅ Manual trigger option
- ✅ Support for variant releases (alpha, beta, test)

## Workflow File

The workflow is located at: `.github/workflows/weekly-release.yml`

## How It Works

### Automated Weekly Releases

Every Monday at 9:00 AM UTC, the workflow:
1. Checks if there are new commits since the last release
2. If yes, calculates the next version number (patch bump)
3. Generates a changelog from commit messages
4. Creates a git tag
5. Creates a GitHub release with the changelog

If there are no new commits, the workflow exits gracefully without creating a release.

### Manual Releases

You can also trigger releases manually via GitHub Actions UI:

1. Go to **Actions** → **Weekly Release**
2. Click **Run workflow**
3. Configure options:
   - **branch**: Target branch (e.g., `v1.2`), defaults to current branch
   - **variant**: Leave empty for prod, or use `alpha`, `beta`, `test` for pre-releases
   - **skip_if_no_changes**: Set to `false` to force release even with no changes

## Versioning

The workflow follows semantic versioning compatible with your existing scheme:

- **Branch format**: `v<major>.<minor>` (e.g., `v1.2`)
- **Prod releases**: `v<major>.<minor>.<patch>` (e.g., `v1.2.3`)
- **Variant releases**: `v<major>.<minor>.<patch>-<variant><number>` (e.g., `v1.2.3-alpha1`)

### Examples

| Scenario | Last Tag | New Tag |
|----------|----------|---------|
| First release on v1.2 | None | `v1.2.0` |
| Weekly release | `v1.2.0` | `v1.2.1` |
| Alpha release | `v1.2.1` | `v1.2.2-alpha1` |
| Second alpha | `v1.2.2-alpha1` | `v1.2.2-alpha2` |

## Changelog Generation

The workflow automatically categorizes commits based on conventional commit prefixes:

- **✨ Features**: Commits starting with `feat:` or `feature:`
- **🐛 Bug Fixes**: Commits starting with `fix:`
- **📚 Documentation**: Commits starting with `docs:`
- **🔧 Other Changes**: All other commits

### Recommended Commit Format

For best changelog results, use conventional commits:

```
feat: add new authentication method
fix: resolve login timeout issue
docs: update API documentation
chore: update dependencies
```

## Schedule Configuration

To change the release schedule, edit the cron expression in `weekly-release.yml`:

```yaml
schedule:
  - cron: '0 9 * * 1'  # Monday at 9 AM UTC
```

Common schedules:
- `0 9 * * 1` - Monday at 9 AM UTC
- `0 9 * * 5` - Friday at 9 AM UTC
- `0 0 * * 1` - Monday at midnight UTC
- `0 9 1,15 * *` - 1st and 15th of month at 9 AM UTC

Use [crontab.guru](https://crontab.guru) to create custom schedules.

## Configuration Options

### Skip Releases When No Changes

By default, the workflow skips releases if there are no commits since the last release. To force weekly releases regardless:

**Option 1**: Manual trigger with `skip_if_no_changes: false`

**Option 2**: Modify the workflow default:
```yaml
skip_if_no_changes:
  default: 'false'  # Change from 'true' to 'false'
```

### Permissions

The workflow requires `contents: write` permission to:
- Create and push git tags
- Create GitHub releases

This is already configured in the workflow file.

## Testing the Workflow

### First Test (Recommended)

1. Create a test variant release:
   - Go to Actions → Weekly Release → Run workflow
   - Set `variant: test`
   - Click "Run workflow"

2. Verify:
   - Tag is created (e.g., `v1.2.3-test1`)
   - GitHub release is created
   - Changelog is generated correctly

### Production Test

Wait for the next Monday at 9 AM UTC, or manually trigger without a variant.

## Troubleshooting

### Workflow doesn't run on schedule

- Check that the workflow file is on your default branch
- Ensure the repository is active (has recent activity)
- GitHub Actions must be enabled for the repository

### No release created

- Check workflow logs for "No changes detected" message
- Verify there are commits since the last release tag
- Ensure branch name matches `v<major>.<minor>` format

### Changelog is empty

- Commits may not follow conventional commit format
- All commits will appear in "Other Changes" section
- Consider adopting conventional commits for better categorization

## Migration from Old Workflow

Your existing `create-release-tag.yml` workflow can coexist with the new workflow:

- **Old workflow**: Keep for emergency manual releases
- **New workflow**: Use for automated weekly releases

Or you can deprecate the old workflow since the new one supports manual triggers with the same functionality.

## Next Steps

1. Review the workflow file
2. Adjust the schedule if needed
3. Test with a variant release
4. Monitor the first automated release
5. Optionally deprecate the old workflow

## Support

For issues or questions:
- Check workflow run logs in GitHub Actions
- Review this documentation
- Consult [GitHub Actions documentation](https://docs.github.com/en/actions)
