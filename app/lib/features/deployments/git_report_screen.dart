import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/deployment.dart' show formatTimestamp, shortCommit;
import '../../models/env_var_group.dart' show kEnvironments;
import '../../models/git_report.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import '../databases/query_download.dart';
import 'deployment_status_screen.dart';
import 'deployment_widgets.dart';
import 'git_commit_widgets.dart';
import 'git_environment_matrix.dart';

/// "Git report": every deployment linked to Git and whether its tracked
/// branch has commits that aren't deployed yet — the fleet-wide version of
/// a deployment's Drift tab, so an admin can list the APIs running old
/// code. Expanding a row shows the undeployed commits with their dates.
/// "By environment" lines each app's deployments up side by side across
/// development, test (QA), staging, and production.
class GitReportScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const GitReportScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<GitReportScreen> createState() => _GitReportScreenState();
}

class _GitReportScreenState extends State<GitReportScreen> {
  late Future<GitReport> _future;
  String? _environment;
  bool _onlyNotUpToDate = true;
  bool _byEnvironment = false;
  final _search = TextEditingController();

  @override
  void initState() {
    super.initState();
    _future = widget.apiClient.getDeploymentGitReport();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  void _check() => setState(() {
    _future = widget.apiClient.getDeploymentGitReport();
  });

  bool _matchesSearch(GitReportRow row) {
    final q = _search.text.trim().toLowerCase();
    return q.isEmpty ||
        row.sourceName.toLowerCase().contains(q) ||
        row.serverName.toLowerCase().contains(q) ||
        row.repository.toLowerCase().contains(q) ||
        row.ref.toLowerCase().contains(q);
  }

  List<GitReportRow> _filtered(GitReport report) => [
    for (final row in report.deployments)
      if ((!_onlyNotUpToDate || row.status != GitReportStatus.upToDate) &&
          (_environment == null || row.environment == _environment) &&
          _matchesSearch(row))
        row,
  ];

  /// "By environment" keeps an app's up-to-date environments (they're
  /// the comparison), and drops only apps that are up to date everywhere.
  List<GitReportApp> _filteredApps(GitReport report) => [
    for (final app in groupGitReportByApp(
      report.deployments.where(_matchesSearch).toList(),
    ))
      if (!_onlyNotUpToDate || !app.allUpToDate) app,
  ];

  Future<void> _export(GitReport report) async {
    final messenger = ScaffoldMessenger.of(context);
    final stamp = formatTimestamp(
      report.checkedAt,
    ).replaceAll(RegExp(r'[^0-9]'), '');
    try {
      final path = _byEnvironment
          ? await downloadQueryCsv(
              gitMatrixCsv(
                _filteredApps(report),
                gitReportEnvironments(report.deployments),
              ),
              'git-report-by-environment-$stamp.csv',
            )
          : await downloadQueryCsv(
              gitReportCsv(_filtered(report)),
              'git-report-$stamp.csv',
            );
      messenger.showSnackBar(SnackBar(content: Text('CSV saved to $path')));
    } catch (e) {
      messenger.showSnackBar(
        SnackBar(content: Text('Could not export CSV: $e')),
      );
    }
  }

  Future<void> _open(GitReportRow row) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DeploymentStatusScreen(
          apiClient: widget.apiClient,
          deploymentId: row.deploymentId,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<GitReport>(
      future: _future,
      builder: (context, snapshot) {
        final loading = snapshot.connectionState != ConnectionState.done;
        final report = loading ? null : snapshot.data;
        final rows = report == null
            ? const <GitReportRow>[]
            : _filtered(report);
        final apps = report == null
            ? const <GitReportApp>[]
            : _filteredApps(report);
        final empty = _byEnvironment ? apps.isEmpty : rows.isEmpty;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            PageIntro(
              description:
                  'Deployments whose Git branch has commits that aren\'t '
                  'deployed yet.',
              summary: report == null ? const [] : _summary(report),
              action: FilledButton.icon(
                onPressed: loading ? null : _check,
                icon: const Icon(Icons.sync),
                label: const Text('Check now'),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
              child: Wrap(
                spacing: 12,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  SegmentedButton<bool>(
                    segments: const [
                      ButtonSegment(
                        value: false,
                        icon: Icon(Icons.view_list_outlined),
                        label: Text('By deployment'),
                      ),
                      ButtonSegment(
                        value: true,
                        icon: Icon(Icons.table_chart_outlined),
                        label: Text('By environment'),
                      ),
                    ],
                    selected: {_byEnvironment},
                    showSelectedIcon: false,
                    onSelectionChanged: (v) =>
                        setState(() => _byEnvironment = v.first),
                  ),
                  FilterSearchField(
                    controller: _search,
                    hint: 'Search API, server, repository, branch',
                    onSubmitted: (_) => setState(() {}),
                  ),
                  if (!_byEnvironment)
                    FilterDropdown<String>(
                      value: _environment,
                      allLabel: 'All environments',
                      options: {
                        for (final e in kEnvironments) e: environmentLabel(e),
                      },
                      onChanged: (v) => setState(() => _environment = v),
                    ),
                  SizedBox(
                    height: kFilterControlHeight,
                    child: FilterChip(
                      label: const Text('Only not up to date'),
                      selected: _onlyNotUpToDate,
                      onSelected: (v) => setState(() => _onlyNotUpToDate = v),
                    ),
                  ),
                  OutlinedButton.icon(
                    onPressed: report == null || empty
                        ? null
                        : () => _export(report),
                    icon: const Icon(Icons.download_outlined),
                    label: const Text('Export CSV'),
                  ),
                  if (report != null)
                    Text(
                      'Checked ${formatWhen(report.checkedAt)}',
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                ],
              ),
            ),
            Expanded(child: _body(snapshot, report, rows, apps)),
          ],
        );
      },
    );
  }

  List<Widget> _summary(GitReport report) {
    int count(String status) =>
        report.deployments.where((r) => r.status == status).length;
    return [
      SummaryStat(
        value: '${report.deployments.length}',
        label: 'linked to Git',
      ),
      SummaryStat(
        value: '${count(GitReportStatus.behind)}',
        label: 'behind',
        color: StatusTone.warning.color,
      ),
      SummaryStat(
        value: '${count(GitReportStatus.pending)}',
        label: 'update waiting',
        color: StatusTone.neutral.color,
      ),
      SummaryStat(
        value: '${count(GitReportStatus.upToDate)}',
        label: 'up to date',
        color: StatusTone.healthy.color,
      ),
      if (count(GitReportStatus.error) > 0)
        SummaryStat(
          value: '${count(GitReportStatus.error)}',
          label: 'can\'t check',
          color: StatusTone.failed.color,
        ),
    ];
  }

  Widget _body(
    AsyncSnapshot<GitReport> snapshot,
    GitReport? report,
    List<GitReportRow> rows,
    List<GitReportApp> apps,
  ) {
    if (snapshot.connectionState != ConnectionState.done) {
      return const Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          CircularProgressIndicator(),
          SizedBox(height: 12),
          Text('Checking each repository for new commits…'),
        ],
      );
    }
    if (snapshot.hasError || report == null) {
      return StateMessage.error(
        what: 'the Git report',
        error: snapshot.error,
        onRetry: _check,
      );
    }
    if (report.deployments.isEmpty) {
      return const StateMessage(
        icon: Icons.link_off,
        title: 'No deployments are linked to Git',
        message:
            'Link a Compose file to a repository in the Git tab, then deploy '
            'it to see it here.',
      );
    }
    if (_byEnvironment ? apps.isEmpty : rows.isEmpty) {
      return StateMessage(
        icon: Icons.check_circle_outline,
        title: _onlyNotUpToDate
            ? 'Every deployment runs the latest code'
            : 'No deployments match',
        message: _onlyNotUpToDate
            ? 'Turn off "Only not up to date" to see them all.'
            : null,
      );
    }
    if (_byEnvironment) {
      return GitEnvironmentMatrix(
        apps: apps,
        environments: gitReportEnvironments(report.deployments),
        onOpenDeployment: _open,
      );
    }
    return RefreshIndicator(
      onRefresh: () async => _check(),
      child: ListView.separated(
        padding: const EdgeInsets.only(bottom: 24),
        itemCount: rows.length,
        separatorBuilder: (_, _) => const Divider(height: 1),
        itemBuilder: (context, i) =>
            _GitReportTile(row: rows[i], onOpen: () => _open(rows[i])),
      ),
    );
  }
}

StatusTone _tone(String status) => switch (status) {
  GitReportStatus.behind => StatusTone.warning,
  GitReportStatus.upToDate => StatusTone.healthy,
  GitReportStatus.error => StatusTone.failed,
  _ => StatusTone.neutral,
};

class _GitReportTile extends StatelessWidget {
  final GitReportRow row;
  final VoidCallback onOpen;

  const _GitReportTile({required this.row, required this.onOpen});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final small = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final waiting = row.waitingFor(DateTime.now());
    final gap = [
      if (row.gapLabel.isNotEmpty) row.gapLabel,
      if (waiting != null) 'oldest waiting ${_days(waiting)}',
    ].join(' · ');
    return ExpansionTile(
      key: PageStorageKey(row.deploymentId),
      tilePadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      expandedCrossAxisAlignment: CrossAxisAlignment.start,
      title: Wrap(
        spacing: 8,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          Text(
            row.sourceName.isEmpty ? row.deploymentId : row.sourceName,
            style: theme.textTheme.titleSmall,
          ),
          StatusPill(
            label: GitReportStatus.labels[row.status] ?? row.status,
            tone: _tone(row.status),
          ),
          if (row.environment.isNotEmpty)
            StatusPill(
              label: environmentLabel(row.environment),
              tone: StatusTone.neutral,
            ),
          if (gap.isNotEmpty) Text(gap, style: small),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              [
                if (row.serverName.isNotEmpty) 'On ${row.serverName}',
                '${row.repository} @ ${row.ref}',
                if (row.phase.isNotEmpty) humanizePhase(row.phase),
              ].join(' · '),
              style: small,
            ),
            if (row.error.isNotEmpty)
              Text(
                'Couldn\'t check: ${row.error}',
                style: small?.copyWith(color: StatusTone.failed.color),
              )
            else ...[
              Text(_deployedLine(row), style: small),
              if (row.isBehind) Text(_latestLine(row), style: small),
            ],
          ],
        ),
      ),
      children: [
        if (row.pendingRequests > 0)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              '${row.pendingRequests} change${row.pendingRequests == 1 ? ' is' : 's are'} '
              'already waiting for approval or a maintenance window.',
            ),
          ),
        if (row.error.isEmpty) ...[
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
        if (row.deployedCommitMissing)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              'The deployed commit is no longer in ${row.ref}\'s history (the '
              'branch may have been force-pushed), so the commits in between '
              'can\'t be listed.',
            ),
          ),
        if (row.commitsBehind.isNotEmpty) ...[
          const SizedBox(height: 8),
          CommitsBehindList(
            commits: row.commitsBehind,
            more: row.moreCommitsBehind,
          ),
        ],
        if (row.detailError.isNotEmpty)
          Text('Commit details unavailable: ${row.detailError}', style: small),
        const SizedBox(height: 8),
        TextButton.icon(
          onPressed: onOpen,
          icon: const Icon(Icons.open_in_new, size: 18),
          label: const Text('Open deployment'),
        ),
      ],
    );
  }

  static String _deployedLine(GitReportRow row) {
    if (row.deployedCommit.isEmpty) return 'Deployed: nothing yet';
    return [
      'Deployed: ${shortCommit(row.deployedCommit)}',
      if (row.deployedAt != null) 'deployed ${formatWhen(row.deployedAt!)}',
    ].join(' · ');
  }

  static String _latestLine(GitReportRow row) {
    final info = row.latestCommitInfo;
    return [
      'Latest: ${shortCommit(row.latestCommit)}',
      if (info != null) 'committed ${formatWhen(info.date)}',
    ].join(' · ');
  }

  static String _days(Duration d) {
    if (d.inDays >= 1) return '${d.inDays} d';
    if (d.inHours >= 1) return '${d.inHours} h';
    return '${d.inMinutes} min';
  }
}

/// The report rows as CSV, one line per deployment, with times in local
/// time as shown on screen.
String gitReportCsv(List<GitReportRow> rows) {
  String cell(String v) =>
      v.contains(RegExp(r'[",\n]')) ? '"${v.replaceAll('"', '""')}"' : v;
  String time(DateTime? t) => t == null ? '' : formatTimestamp(t);
  final lines = [
    'Deployment,Environment,Server,Repository,Branch,Status,Deployed commit,'
        'Deployed at,Deployed commit date,Latest commit,Latest commit date,'
        'Latest commit message,Commits behind,Error',
    for (final r in rows)
      [
        r.sourceName,
        r.environment,
        r.serverName,
        r.repository,
        r.ref,
        GitReportStatus.labels[r.status] ?? r.status,
        shortCommit(r.deployedCommit),
        time(r.deployedAt),
        time(r.deployedCommitInfo?.date),
        shortCommit(r.latestCommit),
        time(r.latestCommitInfo?.date),
        r.latestCommitInfo?.message ?? '',
        r.gapLabel.replaceAll(RegExp(r' commits?'), ''),
        r.error.isNotEmpty ? r.error : r.detailError,
      ].map(cell).join(','),
  ];
  return '${lines.join('\n')}\n';
}
