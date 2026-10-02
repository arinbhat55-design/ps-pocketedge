import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/databases/database_detail_screen.dart';
import 'package:app/features/databases/database_wizard_screen.dart';
import 'package:app/features/databases/postgres_admin_screen.dart';
import 'package:app/models/backup.dart';
import 'package:app/models/database.dart';
import 'package:app/theme/app_theme.dart';

// Covers the database administration additions: backup consistency/format,
// clone jobs, the manage-only actions menu, and the new API client calls.

Map<String, dynamic> _engine(String id, String name) => {
  'id': id,
  'name': name,
  'category': 'relational',
  'description': '$name description',
  'image': id,
  'versions': [
    {'tag': '17', 'label': '17'},
    {'tag': '16', 'label': '16'},
  ],
  'port': {'container': 5432, 'name': 'SQL'},
  'auth': 'password',
  'usernameAllowed': true,
  'defaultUsername': 'dbadmin',
  'databaseNameAllowed': true,
  'databaseNameLabel': 'Database name',
  'architectures': ['amd64', 'arm64'],
  'persistent': true,
  'minMemoryMb': 256,
  'defaultMemoryMb': 1024,
  'defaultCpus': 1,
  'defaultStorageGb': 10,
  'rotation': 'exec',
  'temporaryUsers': true,
  'license': 'MIT',
};

Map<String, dynamic> _instance({String engine = 'postgresql'}) => {
  'id': 'db1',
  'name': 'orders-db',
  'engine': engine,
  'engineName': engine == 'postgresql' ? 'PostgreSQL' : 'MySQL',
  'category': 'relational',
  'version': '17',
  'deploymentId': 'dep1',
  'serverId': 's1',
  'serverName': 'box',
  'databaseName': 'shop',
  'adminUsername': 'dbadmin',
  'port': 55501,
  'access': 'local',
  'profile': 'development',
  'storageGb': 10,
  'memoryMb': 512,
  'cpus': 1,
  'highAvailability': false,
  'adminSecretId': 'sec1',
  'backupConsistent': false,
  'retentionDays': 0,
  'retentionCount': 2,
  'createdAt': '2026-09-25T10:00:00Z',
  'phase': 'running',
  'healthStatus': 'healthy',
  'connectionString': 'postgresql://dbadmin:<password>@127.0.0.1:55501/shop',
  'host': '127.0.0.1',
  'rotation': 'exec',
  'temporaryUsers': true,
  'persistent': true,
  'extraPorts': <Object>[],
};

Map<String, dynamic> _backup(
  String id, {
  bool quiesced = false,
  String format = 'volumes',
}) => {
  'id': id,
  'deploymentId': 'dep1',
  'serverId': 's1',
  'status': 'completed',
  'message': '',
  'origin': 'manual',
  'quiesced': quiesced,
  'format': format,
  'createdAt': '2026-09-26T10:00:00Z',
  'completedAt': '2026-09-26T10:01:00Z',
};

Map<String, dynamic> _detail({
  String engine = 'postgresql',
  bool canManage = true,
  List<Map<String, dynamic>> backups = const [],
  Map<String, dynamic>? clone,
}) => {
  ..._instance(engine: engine),
  'engineInfo': _engine(
    engine,
    engine == 'postgresql' ? 'PostgreSQL' : 'MySQL',
  ),
  'backups': backups,
  'canManage': canManage,
  'clone': ?clone,
};

class _Fake extends http.BaseClient {
  final (int, String) Function(http.BaseRequest) respond;
  _Fake(this.respond);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final (code, body) = respond(request);
    return http.StreamedResponse(
      Stream.value(utf8.encode(body)),
      code,
      headers: {'content-type': 'application/json'},
    );
  }
}

/// Records every request and answers the detail screen's loads; [extra]
/// handles anything else.
class _Recorder {
  final requests = <http.Request>[];
  final Map<String, dynamic> detail;
  final (int, String)? Function(http.Request)? extra;
  _Recorder(this.detail, {this.extra});

  ApiClient client() => ApiClient(
    baseUrl: 'http://localhost',
    httpClient: _Fake((req) {
      final r = req as http.Request;
      requests.add(r);
      final handled = extra?.call(r);
      if (handled != null) return handled;
      switch (r.url.path) {
        case '/api/databases/db1':
          return (200, jsonEncode(detail));
        case '/api/databases/db1/credentials':
          return (200, '[]');
      }
      return (404, '');
    }),
  );

  List<String> get calls => [
    for (final r in requests) '${r.method} ${r.url.path}',
  ];
}

Future<void> _pumpDetail(
  WidgetTester tester,
  ApiClient client, {
  Size size = const Size(400, 1800),
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    MaterialApp(
      theme: AppTheme.dark(),
      home: DatabaseDetailScreen(
        apiClient: client,
        databaseId: 'db1',
        isAdmin: true,
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  group('models', () {
    test('Backup defaults rows written before consistency/format existed', () {
      final legacy = Backup.fromJson({
        'id': 'b1',
        'deploymentId': 'dep1',
        'status': 'completed',
        'createdAt': '2026-09-26T10:00:00Z',
      });
      expect(legacy.quiesced, isFalse);
      expect(legacy.format, 'volumes');

      final logical = Backup.fromJson(_backup('b2', format: 'postgres_custom'));
      expect(logical.format, 'postgres_custom');
      expect(Backup.fromJson(_backup('b3', quiesced: true)).quiesced, isTrue);
    });

    test('DatabaseDetail reads canManage and clone status', () {
      final plain = DatabaseDetail.fromJson(_detail(canManage: false));
      expect(plain.canManage, isFalse);
      expect(plain.cloneStatus, isNull);
      expect(plain.cloneMessage, isNull);

      final cloning = DatabaseDetail.fromJson(
        _detail(clone: {'status': 'dispatched', 'message': 'restoring'}),
      );
      expect(cloning.canManage, isTrue);
      expect(cloning.cloneStatus, 'dispatched');
      expect(cloning.cloneMessage, 'restoring');

      // Older control planes send neither field.
      final older = Map<String, dynamic>.from(_detail())..remove('canManage');
      expect(DatabaseDetail.fromJson(older).canManage, isFalse);
    });

    test('DatabaseRequest sends cloneBackupId only for clones', () {
      DatabaseRequest request({String? clone}) => DatabaseRequest(
        engine: 'postgresql',
        version: '17',
        name: 'orders-db-clone',
        serverId: 's1',
        databaseName: 'shop',
        username: 'dbadmin',
        port: 55502,
        access: 'local',
        profile: 'development',
        storageGb: 10,
        memoryMb: 512,
        cpus: 1,
        highAvailability: false,
        edition: null,
        acceptLicense: false,
        backupCron: '',
        consistentBackups: true,
        retentionDays: 0,
        retentionCount: 0,
        cloneBackupId: clone,
      );
      expect(request().toJson().containsKey('cloneBackupId'), isFalse);
      expect(request(clone: 'b1').toJson()['cloneBackupId'], 'b1');
    });
  });

  group('api client', () {
    late List<http.Request> sent;
    ApiClient client(int code, Object body) {
      sent = [];
      return ApiClient(
        baseUrl: 'http://localhost',
        httpClient: _Fake((req) {
          sent.add(req as http.Request);
          return (code, body is String ? body : jsonEncode(body));
        }),
      );
    }

    test(
      'backupDatabase only sends a body when consistency is overridden',
      () async {
        final api = client(202, {'backupId': 'b9'});
        expect(await api.backupDatabase('db1'), 'b9');
        expect(sent.single.body, isEmpty);

        await api.backupDatabase('db1', consistent: true);
        expect(sent.last.url.path, '/api/databases/db1/backups');
        expect(jsonDecode(sent.last.body), {'consistent': true});
      },
    );

    test('removeDatabase issues DELETE and surfaces errors', () async {
      var api = client(204, '');
      await api.removeDatabase('db1');
      expect(sent.single.method, 'DELETE');
      expect(sent.single.url.path, '/api/databases/db1');

      api = client(409, {'error': 'could not remove database stack: busy'});
      await expectLater(
        api.removeDatabase('db1'),
        throwsA(
          isA<ApiException>().having(
            (e) => e.message,
            'message',
            contains('busy'),
          ),
        ),
      );
    });

    test('refresh, logical export and migration hit their endpoints', () async {
      var api = client(202, {'deploymentId': 'dep1', 'backupId': 'b1'});
      await api.refreshDatabaseFromBackup('db1', 'b1');
      expect(sent.single.url.path, '/api/databases/db1/refresh-from-backup');
      expect(jsonDecode(sent.single.body), {'backupId': 'b1'});

      api = client(202, {'backupId': 'b2'});
      expect(await api.createPostgresLogicalBackup('db1'), 'b2');
      expect(sent.single.url.path, '/api/databases/db1/logical-backups');

      api = client(202, {'deploymentId': 'dep1', 'status': 'dispatched'});
      final outcome = await api.migratePostgresFromBackup('db1', 'b2');
      expect(outcome.isQueued, isFalse);
      expect(sent.single.url.path, '/api/databases/db1/migrate-from-backup');
      expect(jsonDecode(sent.single.body)['backupId'], 'b2');
    });

    test(
      'reconfigureDatabase PATCHes and unwraps the deployment outcome',
      () async {
        final api = client(202, {
          'deployment': {
            'deploymentId': 'dep1',
            'status': 'pending_approval',
            'requestId': 'r1',
          },
          'warnings': <String>[],
        });
        final outcome = await api.reconfigureDatabase(
          'db1',
          version: '17.2',
          memoryMb: 2048,
          cpus: 2,
          storageGb: 20,
        );
        expect(outcome.isQueued, isTrue);
        expect(sent.single.method, 'PATCH');
        expect(sent.single.url.path, '/api/databases/db1/configuration');
        final body = jsonDecode(sent.single.body) as Map<String, dynamic>;
        expect(body['version'], '17.2');
        expect(body['memoryMb'], 2048);
        expect(body['storageGb'], 20);
      },
    );

    test('postgres admin calls', () async {
      var api = client(200, 'current_database\nshop\n');
      expect(await api.postgresQuery('db1', 'SELECT 1'), contains('shop'));
      expect(sent.single.url.path, '/api/databases/db1/postgres/query');

      api = client(201, {'username': 'reporting', 'secretId': 'sec9'});
      expect(
        await api.postgresCreateUser('db1', 'reporting', 'shop', 'read'),
        'sec9',
      );
      expect(jsonDecode(sent.single.body)['permission'], 'read');

      api = client(204, '');
      await api.postgresSetPermission('db1', 'odd name', 'shop', 'none');
      expect(
        sent.single.url.path,
        '/api/databases/db1/postgres/users/odd%20name/permissions',
      );

      api = client(204, '');
      await api.postgresTerminateSession('db1', 4242);
      expect(
        sent.single.url.path,
        '/api/databases/db1/postgres/sessions/4242/terminate',
      );

      api = client(200, [
        {'kind': 'lock_wait', 'severity': 'warning'},
      ]);
      expect((await api.postgresAlerts('db1')).single['kind'], 'lock_wait');
    });
  });

  group('database detail', () {
    testWidgets('app bar fits a phone and exposes PostgreSQL actions', (
      tester,
    ) async {
      final rec = _Recorder(_detail());
      await _pumpDetail(tester, rec.client());
      expect(tester.takeException(), isNull, reason: 'app bar overflowed');

      expect(find.byTooltip('Deployment'), findsOneWidget);
      expect(find.byTooltip('Admin & monitoring'), findsOneWidget);

      await tester.tap(find.byTooltip('Database actions'));
      await tester.pumpAndSettle();
      for (final item in [
        'Version & resources',
        'Create clone instance',
        'Refresh from backup',
        'Export for major migration',
        'Migrate from older version',
        'Remove database',
      ]) {
        expect(find.text(item), findsOneWidget, reason: item);
      }
    });

    testWidgets('wide layouts keep labelled app bar actions', (tester) async {
      final rec = _Recorder(_detail());
      await _pumpDetail(tester, rec.client(), size: const Size(1400, 1400));
      expect(find.widgetWithText(TextButton, 'Deployment'), findsOneWidget);
      expect(
        find.widgetWithText(TextButton, 'Admin & monitoring'),
        findsOneWidget,
      );
    });

    testWidgets('other engines only get generic management actions', (
      tester,
    ) async {
      final rec = _Recorder(_detail(engine: 'mysql'));
      await _pumpDetail(tester, rec.client());
      expect(find.byTooltip('Admin & monitoring'), findsNothing);
      expect(find.byTooltip('More backup options'), findsNothing);

      await tester.tap(find.byTooltip('Database actions'));
      await tester.pumpAndSettle();
      expect(find.text('Version & resources'), findsOneWidget);
      expect(find.text('Remove database'), findsOneWidget);
      expect(find.text('Create clone instance'), findsNothing);
      expect(find.text('Migrate from older version'), findsNothing);
    });

    testWidgets('users who cannot manage get no actions menu', (tester) async {
      final rec = _Recorder(_detail(canManage: false));
      await _pumpDetail(tester, rec.client());
      expect(find.byTooltip('Database actions'), findsNothing);
      // Read-only monitoring stays reachable.
      expect(find.byTooltip('Admin & monitoring'), findsOneWidget);
    });

    testWidgets('clone progress banner follows the clone job', (tester) async {
      var rec = _Recorder(
        _detail(clone: {'status': 'failed', 'message': 'disk full'}),
      );
      await _pumpDetail(tester, rec.client());
      expect(find.text('Clone failed'), findsOneWidget);
      expect(find.text('disk full'), findsOneWidget);

      // Unmount first so the new screen loads fresh instead of reusing state.
      await tester.pumpWidget(const SizedBox());
      rec = _Recorder(_detail(clone: {'status': 'completed', 'message': ''}));
      await _pumpDetail(tester, rec.client());
      expect(find.textContaining('Clone '), findsNothing);
    });

    testWidgets('backups are labelled by format and consistency', (
      tester,
    ) async {
      final rec = _Recorder(
        _detail(
          backups: [
            _backup('b1', quiesced: true),
            _backup('b2', format: 'postgres_custom'),
          ],
        ),
      );
      await _pumpDetail(tester, rec.client());
      expect(find.textContaining('Manual · Consistent'), findsOneWidget);
      expect(find.textContaining('Manual · Logical export'), findsOneWidget);
    });

    testWidgets('consistent backup option overrides the instance policy', (
      tester,
    ) async {
      final rec = _Recorder(
        _detail(),
        extra: (r) =>
            r.method == 'POST' && r.url.path == '/api/databases/db1/backups'
            ? (202, jsonEncode({'backupId': 'b9'}))
            : null,
      );
      await _pumpDetail(tester, rec.client());

      await tester.ensureVisible(find.byTooltip('More backup options'));
      await tester.tap(find.byTooltip('More backup options'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Consistent backup for refresh or upgrade'));
      await tester.pumpAndSettle();

      final backup = rec.requests.singleWhere(
        (r) => r.method == 'POST' && r.url.path == '/api/databases/db1/backups',
      );
      expect(jsonDecode(backup.body), {'consistent': true});
      expect(
        find.textContaining('pauses briefly for a consistent snapshot'),
        findsOneWidget,
      );
    });

    testWidgets('clone without a consistent backup explains what to do', (
      tester,
    ) async {
      final rec = _Recorder(_detail(backups: [_backup('b1')]));
      await _pumpDetail(tester, rec.client());
      await tester.tap(find.byTooltip('Database actions'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Create clone instance'));
      await tester.pumpAndSettle();
      expect(find.textContaining('Take a consistent backup'), findsOneWidget);
      expect(find.byType(DatabaseWizardScreen), findsNothing);
    });

    testWidgets('remove confirms, deletes, and leaves the screen', (
      tester,
    ) async {
      final rec = _Recorder(
        _detail(),
        extra: (r) => r.method == 'DELETE' ? (204, '') : null,
      );
      final client = rec.client();
      tester.view.physicalSize = const Size(400, 1800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.dark(),
          home: Builder(
            builder: (context) => TextButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => DatabaseDetailScreen(
                    apiClient: client,
                    databaseId: 'db1',
                    isAdmin: true,
                  ),
                ),
              ),
              child: const Text('open'),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();

      await tester.tap(find.byTooltip('Database actions'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Remove database'));
      await tester.pumpAndSettle();
      expect(
        find.textContaining('vaulted credentials are deleted'),
        findsOneWidget,
      );
      expect(rec.calls, isNot(contains('DELETE /api/databases/db1')));

      await tester.tap(find.text('Remove'));
      await tester.pumpAndSettle();
      expect(rec.calls, contains('DELETE /api/databases/db1'));
      expect(find.byType(DatabaseDetailScreen), findsNothing);
    });
  });

  testWidgets(
    'clone wizard is prefilled from the source and sends the backup',
    (tester) async {
      final previews = <Map<String, dynamic>>[];
      final client = ApiClient(
        baseUrl: 'http://localhost',
        httpClient: _Fake((req) {
          if (req.url.path == '/api/servers') {
            return (
              200,
              jsonEncode([
                {
                  'id': 's1',
                  'name': 'box',
                  'hostname': 'box',
                  'os': 'linux',
                  'arch': 'arm64',
                  'agentVersion': '1',
                  'status': 'online',
                  'createdAt': '2026-09-25T10:00:00Z',
                },
              ]),
            );
          }
          if (req.url.path == '/api/databases/preview') {
            previews.add(
              jsonDecode((req as http.Request).body) as Map<String, dynamic>,
            );
            return (
              200,
              jsonEncode({
                'composeYaml': 'services: {}',
                'warnings': <String>[],
                'connectionString':
                    'postgresql://dbadmin:<password>@127.0.0.1:55502/shop',
                'username': 'dbadmin',
                'credentials': ['Administrator password'],
                'extraPorts': <Object>[],
              }),
            );
          }
          return (404, '');
        }),
      );
      tester.view.physicalSize = const Size(1400, 1400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);

      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.dark(),
          home: DatabaseWizardScreen(
            apiClient: client,
            engine: DatabaseEngine.fromJson(
              _engine('postgresql', 'PostgreSQL'),
            ),
            cloneSource: DatabaseInstance.fromJson(_instance()),
            cloneBackupId: 'b1',
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('Create clone of orders-db'), findsOneWidget);
      expect(
        find.widgetWithText(TextFormField, 'orders-db-clone'),
        findsOneWidget,
      );

      for (var i = 0; i < 3; i++) {
        await tester.tap(find.text('Continue').hitTestable().first);
        await tester.pumpAndSettle();
      }

      expect(previews, isNotEmpty);
      final sent = previews.last;
      expect(sent['cloneBackupId'], 'b1');
      expect(sent['name'], 'orders-db-clone');
      expect(sent['version'], '17');
      expect(sent['serverId'], 's1');
      expect(sent['databaseName'], 'shop');
      expect(sent['username'], 'dbadmin');
      expect(sent['port'], 55502);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('slow queries explain when insights are not enabled yet', (
    tester,
  ) async {
    final calls = <String>[];
    var insightsEnabled = false;
    final client = ApiClient(
      baseUrl: 'http://localhost',
      httpClient: _Fake((req) {
        calls.add('${req.method} ${req.url.path}');
        const base = '/api/databases/db1/postgres';
        switch (req.url.path) {
          case '$base/overview':
            return (
              200,
              jsonEncode({
                'sampledAt': '2026-09-28T05:03:36Z',
                'serverVersion': '17.2',
                'connectionCount': 1,
                'connectionLimit': -1,
                'transactionsTotal': 10,
                'cacheHitRatio': 0.99,
                'deadlocksTotal': 0,
                'lockWaitCount': 0,
                'databaseBytes': 1024,
                'activeQueryCount': 0,
                'slowQueryCount': 0,
                'longRunningTransactionCount': 0,
              }),
            );
          case '$base/metrics':
            return (
              200,
              jsonEncode({
                'samples': <Object>[],
                'transactionRate': 0,
                'storageGrowthBytes': 0,
              }),
            );
          case '$base/alerts':
          case '$base/sessions':
            return (200, '[]');
          case '$base/sizes':
            return (
              200,
              jsonEncode({'databases': <Object>[], 'tables': <Object>[]}),
            );
          case '$base/slow-queries':
            return insightsEnabled
                ? (200, '[]')
                : (
                    409,
                    jsonEncode({
                      'error':
                          'Query insights are not enabled for this database yet.',
                      'insightsEnabled': false,
                    }),
                  );
          case '$base/query-insights/enable':
            insightsEnabled = true;
            return (204, '');
        }
        return (404, '');
      }),
    );
    tester.view.physicalSize = const Size(1400, 1800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: PostgresAdminScreen(
          api: client,
          database: DatabaseInstance.fromJson(_instance()),
          canManage: true,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();

    expect(
      find.text('Query insights are not enabled for this database yet.'),
      findsOneWidget,
    );
    expect(find.textContaining('ApiException'), findsNothing);
    expect(find.textContaining('Could not load'), findsNothing);

    await tester.ensureVisible(find.text('Enable query insights'));
    await tester.tap(find.text('Enable query insights'));
    await tester.pumpAndSettle();

    expect(
      calls,
      contains('POST /api/databases/db1/postgres/query-insights/enable'),
    );
    expect(find.text('No queries recorded yet.'), findsOneWidget);
    expect(
      find.text('Query insights are not enabled for this database yet.'),
      findsNothing,
    );
  });
}
