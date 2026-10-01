import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:app/api/api_client.dart';
import 'package:app/features/deployments/git_environment_matrix.dart';
import 'package:app/features/deployments/git_report_screen.dart';
import 'package:app/models/deployment.dart' show formatTimestamp;
import 'package:app/models/git_report.dart';

const _old = '1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const _mid = '2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const _new = '3333333ccccccccccccccccccccccccccccccccc';

Map<String, dynamic> _commit(String hash, String message, String date) => {
  'hash': hash,
  'message': message,
  'author': 'Ankita',
  'date': date,
};

final _report = {
  'checkedAt': '2026-10-01T09:00:00Z',
  'deployments': [
    {
      'deploymentId': 'd1',
      'sourceName': 'orders-api',
      'serverName': 'srv-01',
      'environment': 'production',
      'phase': 'running',
      'repository': 'orders',
      'ref': 'main',
      'status': 'behind',
      'deployedCommit': _old,
      'deployedAt': '2026-09-29T10:05:00Z',
      'latestCommit': _new,
      'deployedCommitInfo': _commit(_old, 'Fix login', '2026-09-28T14:32:00Z'),
      'latestCommitInfo': _commit(_new, 'Add caching', '2026-10-01T08:15:00Z'),
      'commitsBehind': [
        _commit(_new, 'Add caching', '2026-10-01T08:15:00Z'),
        _commit(_mid, 'New API endpoint', '2026-09-30T11:02:00Z'),
      ],
    },
    {
      'deploymentId': 'd2',
      'sourceName': 'users-api',
      'serverName': 'srv-01',
      'environment': 'staging',
      'phase': 'running',
      'repository': 'users',
      'ref': 'develop',
      'status': 'pending',
      'pendingRequests': 1,
      'deployedCommit': _mid,
      'latestCommit': _new,
      'commitsBehind': [_commit(_new, 'Bump deps', '2026-09-30T12:30:00Z')],
    },
    {
      'deploymentId': 'd3',
      'sourceName': 'auth-api',
      'serverName': 'srv-03',
      'repository': 'auth',
      'ref': 'main',
      'status': 'up_to_date',
      'deployedCommit': _new,
      'latestCommit': _new,
    },
  ],
};

Map<String, dynamic> _row(
  String id,
  String file,
  String name,
  String env,
  String status,
  String deployed, {
  List<Map<String, dynamic>> behind = const [],
  String ref = 'main',
  String server = 'srv-01',
}) => {
  'deploymentId': id,
  'composeFileId': file,
  'sourceName': name,
  'serverName': server,
  'environment': env,
  'phase': 'running',
  'repository': 'orders',
  'ref': ref,
  'status': status,
  'deployedCommit': deployed,
  'deployedAt': '2026-09-29T10:05:00Z',
  'latestCommit': _new,
  'deployedCommitInfo': _commit(deployed, 'deployed', '2026-09-28T00:00:00Z'),
  'latestCommitInfo': _commit(_new, 'Add caching', '2026-10-01T08:15:00Z'),
  'commitsBehind': behind,
};

final _newCommit = _commit(_new, 'Add caching', '2026-10-01T08:15:00Z');
final _midCommit = _commit(_mid, 'New API endpoint', '2026-09-30T11:02:00Z');

/// orders-api on dev (latest), test/QA (latest), staging (1 behind) and
/// production (2 behind); auth-api up to date everywhere it runs.
final _envReport = {
  'checkedAt': '2026-10-01T09:00:00Z',
  'deployments': [
    _row('o-dev', 'f1', 'orders-api', 'development', 'up_to_date', _new),
    _row('o-test', 'f1', 'orders-api', 'test', 'up_to_date', _new),
    _row(
      'o-stg',
      'f1',
      'orders-api',
      'staging',
      'behind',
      _mid,
      behind: [_newCommit],
    ),
    _row(
      'o-prod',
      'f1',
      'orders-api',
      'production',
      'behind',
      _old,
      behind: [_newCommit, _midCommit],
    ),
    _row('a-prod', 'f2', 'auth-api', 'production', 'up_to_date', _new),
  ],
};

Future<void> _pump(WidgetTester tester, {Map<String, dynamic>? report}) async {
  final client = ApiClient(
    baseUrl: 'http://localhost:8080',
    httpClient: MockClient((req) async {
      if (req.url.path == '/api/deployments/git-report') {
        return http.Response(jsonEncode(report ?? _report), 200);
      }
      return http.Response('not found', 404);
    }),
  );
  tester.view.physicalSize = const Size(1400, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(body: GitReportScreen(apiClient: client)),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('lists deployments behind Git, hiding up-to-date ones', (
    tester,
  ) async {
    await _pump(tester);

    expect(find.text('orders-api'), findsOneWidget);
    expect(find.text('users-api'), findsOneWidget);
    expect(find.text('auth-api'), findsNothing);
    expect(find.text('Behind'), findsOneWidget);
    expect(find.text('Update waiting'), findsOneWidget);
    expect(find.textContaining('2 commits'), findsOneWidget);

    await tester.tap(find.text('Only not up to date'));
    await tester.pumpAndSettle();
    expect(find.text('auth-api'), findsOneWidget);
  });

  testWidgets('expanding a row shows the undeployed commits', (tester) async {
    await _pump(tester);

    await tester.tap(find.text('orders-api'));
    await tester.pumpAndSettle();

    expect(find.text('New API endpoint'), findsOneWidget);
    expect(find.text('Not deployed yet'), findsOneWidget);
    expect(find.text('Open deployment'), findsOneWidget);
  });

  test('CSV has one line per deployment with escaped fields', () {
    final rows = GitReport.fromJson(_report).deployments;
    final csv = gitReportCsv(rows).trim().split('\n');
    expect(csv, hasLength(4));
    expect(
      csv[1],
      startsWith('orders-api,production,srv-01,orders,main,Behind,1111111,'),
    );
    expect(csv[1], contains(',3333333,'));
    expect(csv[1], contains(',Add caching,2,'));
    expect(csv[3], contains('Up to date'));
  });

  test('gap label counts commits and marks a cut-off list', () {
    GitReportRow row(int n, {bool more = false}) => GitReportRow.fromJson({
      'deploymentId': 'd',
      'status': 'behind',
      'deployedCommit': _old,
      'deployedCommitInfo': _commit(_old, 'x', '2026-09-01T00:00:00Z'),
      'commitsBehind': [
        for (var i = 0; i < n; i++)
          _commit(_new, 'c$i', '2026-09-02T00:00:00Z'),
      ],
      'moreCommitsBehind': more,
    });
    expect(row(1).gapLabel, '1 commit');
    expect(row(3).gapLabel, '3 commits');
    expect(row(20, more: true).gapLabel, '20+ commits');
    final missing = GitReportRow.fromJson({
      'deploymentId': 'd',
      'status': 'behind',
      'deployedCommit': _old,
    });
    expect(missing.deployedCommitMissing, isTrue);
    expect(missing.gapLabel, isEmpty);
  });

  testWidgets('by environment lines an app up across environments', (
    tester,
  ) async {
    await _pump(tester, report: _envReport);
    await tester.tap(find.text('By environment'));
    await tester.pumpAndSettle();

    for (final h in ['Development', 'Test (QA)', 'Staging', 'Production']) {
      expect(find.text(h), findsOneWidget);
    }
    expect(find.text('orders-api'), findsOneWidget);
    // auth-api is up to date everywhere, so it's hidden by default.
    expect(find.text('auth-api'), findsNothing);
    expect(find.text('Up to date'), findsNWidgets(2));
    expect(find.text('1 commit behind'), findsOneWidget);
    expect(find.text('2 commits behind'), findsOneWidget);

    await tester.tap(find.text('2 commits behind'));
    await tester.pumpAndSettle();
    expect(find.text('orders-api · Production'), findsOneWidget);
    expect(
      find.textContaining('In Staging, not yet in Production'),
      findsOneWidget,
    );
    expect(
      find.textContaining('promoting would ship 1 commit'),
      findsOneWidget,
    );
  });

  group('commitsAheadOf', () {
    final rows = GitReport.fromJson(_envReport).deployments;
    GitReportRow env(String e) =>
        rows.firstWhere((r) => r.composeFileId == 'f1' && r.environment == e);

    test('lists what the earlier environment would promote', () {
      final ahead = commitsAheadOf(env('staging'), env('production'))!;
      expect(ahead.map((c) => c.hash), [_mid]);
      expect(
        commitsAheadOf(env('test'), env('production'))!.map((c) => c.hash),
        [_new, _mid],
      );
    });

    test('is empty when the earlier environment is not newer', () {
      expect(commitsAheadOf(env('production'), env('staging')), isEmpty);
      expect(commitsAheadOf(env('development'), env('test')), isEmpty);
    });

    test('is unknown across branches', () {
      final other = GitReportRow.fromJson({
        ..._row('x', 'f1', 'orders-api', 'staging', 'behind', _mid),
        'ref': 'develop',
      });
      expect(commitsAheadOf(other, env('production')), isNull);
    });
  });

  test('groups by app with apps needing attention first', () {
    final apps = groupGitReportByApp(
      GitReport.fromJson(_envReport).deployments,
    );
    expect(apps.map((a) => a.name), ['orders-api', 'auth-api']);
    expect(apps.first.representative('production')!.deploymentId, 'o-prod');
    expect(apps.first.representative('qa'), isNull);
    expect(apps.last.allUpToDate, isTrue);
    expect(environmentLabel('test'), 'Test (QA)');
  });

  test('matrix CSV has a column per environment', () {
    final rows = GitReport.fromJson(_envReport).deployments;
    final csv = gitMatrixCsv(
      groupGitReportByApp(rows),
      gitReportEnvironments(rows),
    ).trim().split('\n');
    expect(
      csv.first,
      'API,Latest in Git,Development,Test (QA),Staging,Production',
    );
    expect(csv[1], startsWith('orders-api,main 3333333 ('));
    expect(
      csv[1],
      endsWith(
        '1111111 | deployed '
        '${formatTimestamp(DateTime.parse('2026-09-29T10:05:00Z'))} | 2 commits behind',
      ),
    );
  });
}
