import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/databases/database_detail_screen.dart';
import 'package:app/features/databases/database_marketplace_screen.dart';
import 'package:app/features/databases/database_wizard_screen.dart';
import 'package:app/models/database.dart';
import 'package:app/theme/app_theme.dart';

Map<String, dynamic> engine(
  String id,
  String name,
  String category, {
  String auth = 'password',
}) => {
  'id': id,
  'name': name,
  'category': category,
  'description': '$name description',
  'image': id,
  'versions': [
    {'tag': '1', 'label': '1.0'},
  ],
  'port': {'container': 5432, 'name': 'SQL'},
  'auth': auth,
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

final instance = {
  'id': 'db1',
  'name': 'orders-db',
  'engine': 'postgresql',
  'engineName': 'PostgreSQL',
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
  'backupConsistent': true,
  'retentionDays': 0,
  'retentionCount': 2,
  'backupCron': '0 2 * * *',
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

final credential = {
  'id': 'sec1',
  'name': 'Administrator password',
  'kind': 'admin',
  'username': 'dbadmin',
  'version': 1,
  'createdAt': '2026-09-25T10:00:00Z',
  'masked': '••••••••••••',
  'active': true,
  'canReveal': true,
  'canManage': true,
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

Finder selectable(String text) =>
    find.byWidgetPredicate((w) => w is SelectableText && w.data == text);

void main() {
  test('models parse the API shapes', () {
    final e = DatabaseEngine.fromJson(engine('redis', 'Redis', 'cache'));
    expect(e.hasCredentials, isTrue);
    expect(e.supportsHighAvailability, isFalse);
    final d = DatabaseInstance.fromJson(instance);
    expect(d.backupCron, '0 2 * * *');
    expect(d.cpus, 1.0);
    final c = DatabaseCredential.fromJson(credential);
    expect(c.canReveal, isTrue);
    expect(c.isTemporary, isFalse);
  });

  testWidgets('catalog lists engines and filters by category', (tester) async {
    final client = ApiClient(
      baseUrl: 'http://localhost',
      httpClient: _Fake((req) {
        if (req.url.path == '/api/database-engines') {
          return (
            200,
            jsonEncode([
              engine('postgresql', 'PostgreSQL', 'relational'),
              engine('redis', 'Redis', 'cache'),
              engine('chroma', 'Chroma', 'vector', auth: 'none'),
            ]),
          );
        }
        return (200, '[]');
      }),
    );
    tester.view.physicalSize = const Size(1400, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: DatabaseMarketplaceScreen(apiClient: client, isAdmin: true),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('PostgreSQL'), findsOneWidget);
    expect(find.text('Redis'), findsOneWidget);
    expect(find.text('No auth'), findsOneWidget);

    await tester.tap(find.text('Cache & key-value (1)'));
    await tester.pumpAndSettle();
    expect(find.text('Redis'), findsOneWidget);
    expect(find.text('PostgreSQL'), findsNothing);

    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('credentials are masked until revealed', (tester) async {
    final requests = <String>[];
    final client = ApiClient(
      baseUrl: 'http://localhost',
      httpClient: _Fake((req) {
        requests.add('${req.method} ${req.url.path}');
        switch (req.url.path) {
          case '/api/databases/db1':
            return (
              200,
              jsonEncode({
                ...instance,
                'engineInfo': engine('postgresql', 'PostgreSQL', 'relational'),
                'backups': <Object>[],
              }),
            );
          case '/api/databases/db1/credentials':
            return (200, jsonEncode([credential]));
          case '/api/secrets/sec1/reveal':
            return (
              200,
              jsonEncode({
                'username': 'dbadmin',
                'value': 'Sup3rSecretValue',
                'version': 1,
              }),
            );
        }
        return (404, '');
      }),
    );
    tester.view.physicalSize = const Size(400, 1800);
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

    expect(find.text('Administrator password'), findsOneWidget);
    expect(selectable('••••••••••••'), findsOneWidget);
    expect(selectable('Sup3rSecretValue'), findsNothing);
    expect(requests, isNot(contains('POST /api/secrets/sec1/reveal')));

    await tester.tap(find.byTooltip('Reveal for 30 seconds'));
    await tester.pumpAndSettle();
    expect(selectable('Sup3rSecretValue'), findsOneWidget);
    expect(requests, contains('POST /api/secrets/sec1/reveal'));

    // Masked again after the reveal window.
    await tester.pump(const Duration(seconds: 31));
    await tester.pumpAndSettle();
    expect(selectable('Sup3rSecretValue'), findsNothing);

    await tester.pumpWidget(const SizedBox());
  });

  for (final size in const [Size(1400, 1400), Size(400, 900)]) {
    testWidgets('wizard walks to a validated review at ${size.width}px', (
      tester,
    ) async {
      final bodies = <Map<String, dynamic>>[];
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
            bodies.add(
              jsonDecode((req as http.Request).body) as Map<String, dynamic>,
            );
            return (
              200,
              jsonEncode({
                'composeYaml': 'services: {}',
                'warnings': ['storage is not enforced'],
                'connectionString':
                    'postgresql://dbadmin:<password>@127.0.0.1:5432/app',
                'username': 'dbadmin',
                'credentials': ['Administrator password'],
                'extraPorts': <Object>[],
              }),
            );
          }
          return (404, '');
        }),
      );
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);

      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.dark(),
          home: DatabaseWizardScreen(
            apiClient: client,
            engine: DatabaseEngine.fromJson(
              engine('postgresql', 'PostgreSQL', 'relational'),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Basics: name and server are required.
      await tester.tap(find.text('Continue').hitTestable().first);
      await tester.pumpAndSettle();
      expect(
        find.text('3-40 characters, starting with a letter'),
        findsOneWidget,
      );

      await tester.enterText(
        find.widgetWithText(TextFormField, 'Instance name'),
        'orders-db',
      );
      await tester.tap(find.text('Server'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('box (arm64)').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('Production'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Continue').hitTestable().first);
      await tester.pumpAndSettle();

      // Resources → Backups (production preset daily) → Review.
      await tester.tap(find.text('Continue').hitTestable().first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('Continue').hitTestable().first);
      await tester.pumpAndSettle();

      expect(find.text('Credentials are generated for you'), findsOneWidget);
      expect(bodies, hasLength(1));
      final sent = bodies.single;
      expect(sent['name'], 'orders-db');
      expect(sent['serverId'], 's1');
      expect(sent['profile'], 'production');
      expect(sent['access'], 'local');
      expect((sent['backup'] as Map)['cron'], '0 2 * * *');
      expect((sent['backup'] as Map)['retentionDays'], 7);
      expect(tester.takeException(), isNull);
    });
  }
}
