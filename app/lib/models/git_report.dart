import 'env_var_group.dart' show kEnvironments;
import 'git_repository.dart';

/// GET /api/deployments/git-report: for every deployment linked to Git,
/// whether its tracked branch has commits that aren't deployed yet. Rows
/// come sorted with the ones needing attention first.
class GitReport {
  final DateTime checkedAt;
  final List<GitReportRow> deployments;

  const GitReport({required this.checkedAt, required this.deployments});

  factory GitReport.fromJson(Map<String, dynamic> json) {
    return GitReport(
      checkedAt: DateTime.parse(json['checkedAt'] as String),
      deployments: (json['deployments'] as List<dynamic>? ?? [])
          .map((e) => GitReportRow.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// [GitReportRow.status] values.
abstract final class GitReportStatus {
  static const behind = 'behind';
  static const pending = 'pending';
  static const error = 'error';
  static const notDeployed = 'not_deployed';
  static const upToDate = 'up_to_date';

  static const labels = {
    behind: 'Behind',
    pending: 'Update waiting',
    error: 'Can\'t check',
    notDeployed: 'Not deployed',
    upToDate: 'Up to date',
  };
}

/// One deployment's Git status. [deployedAt] is when its current revision
/// was rolled out; the commit details and [commitsBehind] (newest first)
/// are absent when the repository's history couldn't be read
/// ([detailError] says why).
class GitReportRow {
  final String deploymentId;
  final String composeFileId;
  final String sourceName;
  final String serverName;
  final String environment;
  final String phase;
  final String repository;
  final String ref;
  final String status;
  final int pendingRequests;
  final String deployedCommit;
  final DateTime? deployedAt;
  final String latestCommit;
  final GitCommit? deployedCommitInfo;
  final GitCommit? latestCommitInfo;
  final List<GitCommit> commitsBehind;
  final bool moreCommitsBehind;
  final String detailError;
  final String error;

  const GitReportRow({
    required this.deploymentId,
    this.composeFileId = '',
    this.sourceName = '',
    this.serverName = '',
    this.environment = '',
    this.phase = '',
    this.repository = '',
    this.ref = '',
    required this.status,
    this.pendingRequests = 0,
    this.deployedCommit = '',
    this.deployedAt,
    this.latestCommit = '',
    this.deployedCommitInfo,
    this.latestCommitInfo,
    this.commitsBehind = const [],
    this.moreCommitsBehind = false,
    this.detailError = '',
    this.error = '',
  });

  factory GitReportRow.fromJson(Map<String, dynamic> json) {
    GitCommit? commit(Object? v) =>
        v == null ? null : GitCommit.fromJson(v as Map<String, dynamic>);
    final deployedAt = json['deployedAt'] as String?;
    return GitReportRow(
      deploymentId: json['deploymentId'] as String,
      composeFileId: json['composeFileId'] as String? ?? '',
      sourceName: json['sourceName'] as String? ?? '',
      serverName: json['serverName'] as String? ?? '',
      environment: json['environment'] as String? ?? '',
      phase: json['phase'] as String? ?? '',
      repository: json['repository'] as String? ?? '',
      ref: json['ref'] as String? ?? '',
      status: json['status'] as String? ?? GitReportStatus.error,
      pendingRequests: (json['pendingRequests'] as num?)?.toInt() ?? 0,
      deployedCommit: json['deployedCommit'] as String? ?? '',
      deployedAt: deployedAt == null ? null : DateTime.parse(deployedAt),
      latestCommit: json['latestCommit'] as String? ?? '',
      deployedCommitInfo: commit(json['deployedCommitInfo']),
      latestCommitInfo: commit(json['latestCommitInfo']),
      commitsBehind: (json['commitsBehind'] as List<dynamic>? ?? [])
          .map((e) => GitCommit.fromJson(e as Map<String, dynamic>))
          .toList(),
      moreCommitsBehind: json['moreCommitsBehind'] as bool? ?? false,
      detailError: json['detailError'] as String? ?? '',
      error: json['error'] as String? ?? '',
    );
  }

  /// Behind the branch, whether or not an update is already waiting.
  bool get isBehind =>
      status == GitReportStatus.behind || status == GitReportStatus.pending;

  /// The deployed commit is no longer in the branch's history (force-push
  /// or rebase), so the commits in between can't be counted.
  bool get deployedCommitMissing =>
      isBehind && detailError.isEmpty && deployedCommitInfo == null;

  /// "3 commits", "20+ commits", or empty when unknown / up to date.
  String get gapLabel {
    if (!isBehind || deployedCommitMissing || detailError.isNotEmpty) {
      return '';
    }
    final n = commitsBehind.length;
    return '$n${moreCommitsBehind ? '+' : ''} commit${n == 1 && !moreCommitsBehind ? '' : 's'}';
  }

  /// How long the oldest undeployed commit has been waiting.
  Duration? waitingFor(DateTime now) {
    if (!isBehind || commitsBehind.isEmpty) return null;
    return now.difference(commitsBehind.last.date);
  }
}

/// An environment's name for column headers and pills. "test" doubles as
/// the QA environment, so it reads "Test (QA)".
String environmentLabel(String environment) => switch (environment) {
  '' => 'No environment',
  'test' => 'Test (QA)',
  _ => environment[0].toUpperCase() + environment.substring(1),
};

/// The commits [ahead] runs that [behind] doesn't — what promoting
/// [ahead]'s version to [behind]'s environment would ship, newest first.
/// Empty when [ahead] isn't newer; null when it can't be told: they track
/// different branches, either couldn't be checked, or [behind]'s commit
/// list was cut off before reaching [ahead]'s commit.
List<GitCommit>? commitsAheadOf(GitReportRow ahead, GitReportRow behind) {
  if (ahead.ref != behind.ref ||
      ahead.deployedCommit.isEmpty ||
      behind.deployedCommit.isEmpty ||
      ahead.error.isNotEmpty ||
      behind.error.isNotEmpty ||
      behind.detailError.isNotEmpty) {
    return null;
  }
  if (ahead.deployedCommit == behind.deployedCommit) return const [];
  final i = behind.commitsBehind.indexWhere(
    (c) => c.hash == ahead.deployedCommit,
  );
  if (i >= 0) return behind.commitsBehind.sublist(i);
  // Not among the commits [behind] is missing: [ahead] is older (or the
  // same age), unless the list was cut off or [behind]'s commit is no
  // longer on the branch.
  if (behind.moreCommitsBehind || behind.deployedCommitMissing) return null;
  return const [];
}

/// One app (Compose file) across environments: the "By environment" row of
/// the Git report. [byEnvironment] maps an environment ('' for none) to its
/// deployments, most behind first.
class GitReportApp {
  final String composeFileId;
  final String name;
  final Map<String, List<GitReportRow>> byEnvironment;

  const GitReportApp({
    required this.composeFileId,
    required this.name,
    required this.byEnvironment,
  });

  Iterable<GitReportRow> get rows => byEnvironment.values.expand((r) => r);

  /// The tip of each branch this app's deployments track, as a row that
  /// has it (for its commit details), keyed by branch.
  Map<String, GitReportRow> get latestByRef {
    final out = <String, GitReportRow>{};
    for (final r in rows) {
      if (r.latestCommit.isEmpty) continue;
      final seen = out[r.ref];
      if (seen == null ||
          (seen.latestCommitInfo == null && r.latestCommitInfo != null)) {
        out[r.ref] = r;
      }
    }
    return out;
  }

  /// Every deployment is on its branch's latest commit.
  bool get allUpToDate =>
      rows.every((r) => r.status == GitReportStatus.upToDate);

  /// The deployment shown for [environment]: the one furthest behind, or
  /// null when the app isn't deployed there.
  GitReportRow? representative(String environment) {
    final list = byEnvironment[environment];
    return list == null || list.isEmpty ? null : list.first;
  }

  /// Deployments in [environment] that run different commits.
  bool serversDiffer(String environment) =>
      (byEnvironment[environment] ?? const [])
          .map((r) => r.deployedCommit)
          .toSet()
          .length >
      1;
}

/// The environments, in promotion order, that the "By environment" view
/// shows as columns: the standard four, then "no environment" when any
/// deployment has none, then any other names in use.
List<String> gitReportEnvironments(List<GitReportRow> rows) {
  final used = rows.map((r) => r.environment).toSet();
  return [
    ...kEnvironments,
    if (used.contains('')) '',
    ...(used.difference({...kEnvironments, ''}).toList()..sort()),
  ];
}

int _behindRank(GitReportRow r) {
  if (r.error.isNotEmpty) return 0;
  if (!r.isBehind) return 1000000;
  return 1000 - r.commitsBehind.length - (r.moreCommitsBehind ? 1 : 0);
}

/// Groups report rows by app (Compose file), apps needing attention first.
List<GitReportApp> groupGitReportByApp(List<GitReportRow> rows) {
  final byFile = <String, List<GitReportRow>>{};
  for (final r in rows) {
    byFile.putIfAbsent(r.composeFileId, () => []).add(r);
  }
  final apps = [
    for (final e in byFile.entries)
      GitReportApp(
        composeFileId: e.key,
        name: e.value.first.sourceName,
        byEnvironment: {
          for (final env in e.value.map((r) => r.environment).toSet())
            env: e.value.where((r) => r.environment == env).toList()
              ..sort((a, b) => _behindRank(a).compareTo(_behindRank(b))),
        },
      ),
  ];
  apps.sort((a, b) {
    if (a.allUpToDate != b.allUpToDate) return a.allUpToDate ? 1 : -1;
    return a.name.toLowerCase().compareTo(b.name.toLowerCase());
  });
  return apps;
}
