import 'package:flutter/material.dart';

import '../../models/deployment.dart' show formatTimestamp, shortCommit;
import '../../models/git_report.dart';
import '../../widgets/state_message.dart' show kTableMinWidth;
import '../../widgets/status_pill.dart';
import 'git_commit_widgets.dart';

/// The Git report "By environment": one row per app (Compose file), one
/// column per environment, each cell showing which commit runs there, when
/// it was deployed, and how far behind its branch it is. Tapping a cell
/// lists what it's missing — compared with the branch, and with the
/// environment before it (what a promotion would ship).
class GitEnvironmentMatrix extends StatelessWidget {
  final List<GitReportApp> apps;
  final List<String> environments;
  final ValueChanged<GitReportRow> onOpenDeployment;

  const GitEnvironmentMatrix({
    super.key,
    required this.apps,
    required this.environments,
    required this.onOpenDeployment,
  });

  static const _appWidth = 180.0;
  static const _cellWidth = 190.0;

  void _showCell(BuildContext context, GitReportApp app, String env) {
    showDialog<void>(
      context: context,
      builder: (_) => _CellDetailsDialog(
        app: app,
        environment: env,
        previous: _previousEnvironment(app, env),
        onOpenDeployment: onOpenDeployment,
      ),
    );
  }

  /// The nearest environment to the left of [env] where [app] is deployed —
  /// where its next version normally comes from.
  String? _previousEnvironment(GitReportApp app, String env) {
    final i = environments.indexOf(env);
    for (var j = i - 1; j >= 0; j--) {
      if (app.representative(environments[j]) != null) return environments[j];
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) => constraints.maxWidth < kTableMinWidth
          ? _cards(context)
          : _table(context),
    );
  }

  Widget _table(BuildContext context) {
    final theme = Theme.of(context);
    final border = BorderSide(color: theme.colorScheme.outlineVariant);
    Widget header(String text) => Padding(
      padding: const EdgeInsets.all(10),
      child: Text(text, style: theme.textTheme.labelLarge),
    );
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Table(
          defaultColumnWidth: const FixedColumnWidth(_cellWidth),
          columnWidths: const {0: FixedColumnWidth(_appWidth)},
          border: TableBorder(
            horizontalInside: border,
            verticalInside: border,
            top: border,
            bottom: border,
            left: border,
            right: border,
          ),
          defaultVerticalAlignment: TableCellVerticalAlignment.top,
          children: [
            TableRow(
              decoration: BoxDecoration(
                color: theme.colorScheme.surfaceContainerHigh,
              ),
              children: [
                header('API'),
                header('Latest in Git'),
                for (final env in environments) header(environmentLabel(env)),
              ],
            ),
            for (final app in apps)
              TableRow(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(10),
                    child: Text(app.name, style: theme.textTheme.titleSmall),
                  ),
                  Padding(
                    padding: const EdgeInsets.all(10),
                    child: _LatestCell(app: app),
                  ),
                  for (final env in environments)
                    _EnvironmentCell(
                      app: app,
                      environment: env,
                      onTap: () => _showCell(context, app, env),
                    ),
                ],
              ),
          ],
        ),
      ),
    );
  }

  Widget _cards(BuildContext context) {
    final theme = Theme.of(context);
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      children: [
        for (final app in apps)
          Card(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
                    child: Text(app.name, style: theme.textTheme.titleSmall),
                  ),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
                    child: _LatestCell(app: app),
                  ),
                  for (final env in environments)
                    if (app.representative(env) != null || env.isNotEmpty)
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Padding(
                            padding: const EdgeInsets.fromLTRB(16, 10, 0, 0),
                            child: SizedBox(
                              width: 96,
                              child: Text(
                                environmentLabel(env),
                                style: theme.textTheme.labelMedium,
                              ),
                            ),
                          ),
                          Expanded(
                            child: _EnvironmentCell(
                              app: app,
                              environment: env,
                              onTap: () => _showCell(context, app, env),
                            ),
                          ),
                        ],
                      ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

/// The tip of each branch the app's deployments track.
class _LatestCell extends StatelessWidget {
  final GitReportApp app;

  const _LatestCell({required this.app});

  @override
  Widget build(BuildContext context) {
    final small = Theme.of(context).textTheme.bodySmall?.copyWith(
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    );
    final latest = app.latestByRef;
    if (latest.isEmpty) return Text('—', style: small);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final e in latest.entries) ...[
          Text.rich(
            TextSpan(
              children: [
                TextSpan(text: '${e.key}  ', style: small),
                TextSpan(
                  text: shortCommit(e.value.latestCommit),
                  style: const TextStyle(fontFamily: 'monospace'),
                ),
              ],
            ),
          ),
          if (e.value.latestCommitInfo != null)
            Text(
              'committed ${formatTimestamp(e.value.latestCommitInfo!.date)}',
              style: small,
            ),
        ],
      ],
    );
  }
}

/// One app in one environment: its deployed commit, when, and how far
/// behind. Shows the furthest-behind deployment when there are several.
class _EnvironmentCell extends StatelessWidget {
  final GitReportApp app;
  final String environment;
  final VoidCallback onTap;

  const _EnvironmentCell({
    required this.app,
    required this.environment,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final small = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final row = app.representative(environment);
    if (row == null) {
      return Padding(
        padding: const EdgeInsets.all(10),
        child: Text('— not deployed', style: small),
      );
    }
    final count = app.byEnvironment[environment]!.length;
    final tone = cellTone(row);
    return InkWell(
      onTap: onTap,
      child: Container(
        color: tone == StatusTone.healthy
            ? null
            : tone.color.withValues(alpha: 0.06),
        padding: const EdgeInsets.all(10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(_icon(row), size: 16, color: tone.color),
                const SizedBox(width: 6),
                Text(
                  row.deployedCommit.isEmpty
                      ? 'nothing yet'
                      : shortCommit(row.deployedCommit),
                  style: const TextStyle(fontFamily: 'monospace'),
                ),
              ],
            ),
            if (row.deployedAt != null)
              Text(
                'deployed ${formatTimestamp(row.deployedAt!)}',
                style: small,
              ),
            Text(
              cellStatusText(row),
              style: small?.copyWith(color: tone.color),
            ),
            if (count > 1)
              Text(
                app.serversDiffer(environment)
                    ? '$count servers · on different commits'
                    : '$count servers',
                style: small?.copyWith(
                  color: app.serversDiffer(environment)
                      ? StatusTone.warning.color
                      : null,
                ),
              ),
          ],
        ),
      ),
    );
  }

  static IconData _icon(GitReportRow row) => switch (row.status) {
    GitReportStatus.upToDate => Icons.check_circle_outline,
    GitReportStatus.pending => Icons.hourglass_top,
    GitReportStatus.error => Icons.error_outline,
    GitReportStatus.notDeployed => Icons.remove_circle_outline,
    _ => Icons.warning_amber,
  };
}

StatusTone cellTone(GitReportRow row) => switch (row.status) {
  GitReportStatus.upToDate => StatusTone.healthy,
  GitReportStatus.behind => StatusTone.warning,
  GitReportStatus.error => StatusTone.failed,
  _ => StatusTone.neutral,
};

/// "Up to date", "3 commits behind", "1 commit behind · update waiting"…
String cellStatusText(GitReportRow row) {
  switch (row.status) {
    case GitReportStatus.upToDate:
      return 'Up to date';
    case GitReportStatus.error:
      return 'Can\'t check';
    case GitReportStatus.notDeployed:
      return 'No commit deployed yet';
  }
  final gap = row.gapLabel.isEmpty ? 'Behind' : '${row.gapLabel} behind';
  return row.status == GitReportStatus.pending ? '$gap · update waiting' : gap;
}

class _CellDetailsDialog extends StatelessWidget {
  final GitReportApp app;
  final String environment;
  final String? previous;
  final ValueChanged<GitReportRow> onOpenDeployment;

  const _CellDetailsDialog({
    required this.app,
    required this.environment,
    required this.previous,
    required this.onOpenDeployment,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final small = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final rows = app.byEnvironment[environment]!;
    final rep = rows.first;
    final prev = previous == null ? null : app.representative(previous!);
    return AlertDialog(
      title: Text('${app.name} · ${environmentLabel(environment)}'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              for (final row in rows) ...[
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        [
                          if (row.serverName.isNotEmpty) 'On ${row.serverName}',
                          '${row.repository} @ ${row.ref}',
                        ].join(' · '),
                        style: small,
                      ),
                    ),
                    StatusPill(label: cellStatusText(row), tone: cellTone(row)),
                  ],
                ),
                if (row.error.isNotEmpty)
                  Text('Couldn\'t check: ${row.error}')
                else ...[
                  CommitSummaryLine(
                    label: 'Deployed',
                    commit: row.deployedCommit,
                    info: row.deployedCommitInfo,
                    deployedAt: row.deployedAt,
                  ),
                  if (row.isBehind)
                    CommitSummaryLine(
                      label: 'Latest',
                      commit: row.latestCommit,
                      info: row.latestCommitInfo,
                    ),
                ],
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    onPressed: () {
                      Navigator.of(context).pop();
                      onOpenDeployment(row);
                    },
                    icon: const Icon(Icons.open_in_new, size: 18),
                    label: const Text('Open deployment'),
                  ),
                ),
                const Divider(),
              ],
              if (prev != null) ...[
                _PromotionDiff(
                  from: previous!,
                  to: environment,
                  ahead: prev,
                  behind: rep,
                ),
                const SizedBox(height: 12),
              ],
              if (rep.commitsBehind.isNotEmpty)
                CommitsBehindList(
                  title:
                      'On ${rep.ref} but not in ${environmentLabel(environment)}',
                  commits: rep.commitsBehind,
                  more: rep.moreCommitsBehind,
                )
              else if (rep.deployedCommitMissing)
                Text(
                  'The deployed commit is no longer in ${rep.ref}\'s history '
                  '(the branch may have been force-pushed), so the commits in '
                  'between can\'t be listed.',
                  style: small,
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
      ],
    );
  }
}

/// What the previous environment runs that this one doesn't.
class _PromotionDiff extends StatelessWidget {
  final String from;
  final String to;
  final GitReportRow ahead;
  final GitReportRow behind;

  const _PromotionDiff({
    required this.from,
    required this.to,
    required this.ahead,
    required this.behind,
  });

  @override
  Widget build(BuildContext context) {
    final fromLabel = environmentLabel(from);
    final toLabel = environmentLabel(to);
    final commits = commitsAheadOf(ahead, behind);
    if (commits == null) {
      return Text(
        ahead.ref != behind.ref
            ? 'Compared with $fromLabel: they track different branches '
                  '(${ahead.ref} vs ${behind.ref}), so they can\'t be compared '
                  'commit by commit.'
            : 'Compared with $fromLabel: can\'t tell which commits differ.',
      );
    }
    if (commits.isEmpty) {
      return Text(
        ahead.deployedCommit == behind.deployedCommit
            ? '$toLabel runs the same commit as $fromLabel.'
            : '$fromLabel has nothing that $toLabel doesn\'t.',
      );
    }
    return CommitsBehindList(
      title:
          'In $fromLabel, not yet in $toLabel — promoting would ship '
          '${commits.length} commit${commits.length == 1 ? '' : 's'}',
      commits: commits,
    );
  }
}

/// The "By environment" view as CSV: one line per app, one column per
/// environment ("abc1234 | deployed 2026-09-29 10:05 | 3 commits behind").
String gitMatrixCsv(List<GitReportApp> apps, List<String> environments) {
  String cell(String v) =>
      v.contains(RegExp(r'[",\n]')) ? '"${v.replaceAll('"', '""')}"' : v;
  final lines = [
    [
      'API',
      'Latest in Git',
      ...environments.map(environmentLabel),
    ].map(cell).join(','),
    for (final app in apps)
      [
        app.name,
        [
          for (final e in app.latestByRef.entries)
            '${e.key} ${shortCommit(e.value.latestCommit)}'
                '${e.value.latestCommitInfo == null ? '' : ' (${formatTimestamp(e.value.latestCommitInfo!.date)})'}',
        ].join('; '),
        for (final env in environments)
          () {
            final row = app.representative(env);
            if (row == null) return '';
            return [
              if (row.deployedCommit.isNotEmpty)
                shortCommit(row.deployedCommit),
              if (row.deployedAt != null)
                'deployed ${formatTimestamp(row.deployedAt!)}',
              cellStatusText(row),
              if (app.byEnvironment[env]!.length > 1)
                '${app.byEnvironment[env]!.length} servers',
            ].join(' | ');
          }(),
      ].map(cell).join(','),
  ];
  return '${lines.join('\n')}\n';
}
