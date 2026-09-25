import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/widgets/state_message.dart';
import 'package:app/features/containers/container_list_screen.dart';
import 'package:app/features/containers/container_table.dart';

Map<String, dynamic> _container(String id, String name, String image) => {
  'serverId': 's1',
  'serverName': 'edge-1',
  'containerId': id,
  'name': name,
  'state': 'running',
  'image': image,
};

void main() {
  connectivityTests();
  late List<Uri> requests;

  ApiClient clientReturning(List<Map<String, dynamic>> containers) {
    requests = [];
    return ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeClient((uri) {
        requests.add(uri);
        if (uri.path != '/api/containers') return '[]';
        // Server-side name filter, like the control plane.
        final name = uri.queryParameters['name'];
        return jsonEncode([
          for (final c in containers)
            if (name == null || (c['name'] as String).contains(name)) c,
        ]);
      }),
    );
  }

  Future<void> pumpAt(WidgetTester tester, ApiClient client, Size size) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      MaterialApp(home: ContainerListScreen(apiClient: client)),
    );
    await tester.pumpAndSettle();
  }

  testWidgets(
    'a search with no results keeps filter choices and offers Clear filters',
    (tester) async {
      final client = clientReturning([
        _container('aaa', 'web', 'nginx:latest'),
        _container('bbb', 'db', 'postgres:16'),
      ]);
      await pumpAt(tester, client, const Size(1280, 900));

      await tester.enterText(find.byType(TextField), 'nomatch');
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();

      expect(find.text('No containers match'), findsOneWidget);
      // Choices come from the unfiltered listing, so the Image filter is
      // still there even though the current results are empty.
      expect(find.text('Image:'), findsOneWidget);

      await tester.tap(find.widgetWithText(FilledButton, 'Clear filters'));
      await tester.pumpAndSettle();

      expect(find.text('web'), findsOneWidget);
      expect(find.text('db'), findsOneWidget);
      expect(find.text('Clear filters'), findsNothing);
    },
  );

  testWidgets('phones get cards instead of the wide table', (tester) async {
    final client = clientReturning([_container('aaa', 'web', 'nginx:latest')]);
    await pumpAt(tester, client, const Size(390, 844));

    expect(find.byType(ContainerTable), findsNothing);
    expect(find.byType(Card), findsOneWidget);
    expect(find.text('web'), findsOneWidget);
    expect(find.byTooltip('Stop'), findsOneWidget);
  });

  testWidgets('desktop keeps the table', (tester) async {
    final client = clientReturning([_container('aaa', 'web', 'nginx:latest')]);
    await pumpAt(tester, client, const Size(1280, 900));

    expect(find.byType(ContainerTable), findsOneWidget);
    // Actions are visible without any horizontal scrolling.
    expect(find.byTooltip('Remove'), findsOneWidget);
  });

  testWidgets('empty fleet explains and offers Create container', (
    tester,
  ) async {
    await pumpAt(tester, clientReturning([]), const Size(1280, 900));

    expect(find.text('No containers yet'), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(StateMessage),
        matching: find.widgetWithText(FilledButton, 'Create container'),
      ),
      findsOneWidget,
    );
  });
}

void connectivityTests() {
  testWidgets(
    'containers on a disconnected server read as Unknown, not Running',
    (tester) async {
      final old = DateTime.now().subtract(const Duration(days: 3));
      final fresh = DateTime.now();
      Map<String, dynamic> server(String id, DateTime hb) => {
        'id': id,
        'name': id,
        'hostname': id,
        'os': 'linux',
        'arch': 'amd64',
        'agentVersion': '1',
        'status': 'online',
        'lastHeartbeatAt': hb.toUtc().toIso8601String(),
        'lastResources': null,
        'createdAt': fresh.toUtc().toIso8601String(),
      };
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        httpClient: _FakeClient((uri) {
          if (uri.path == '/api/servers') {
            return jsonEncode([server('gone', old), server('live', fresh)]);
          }
          if (uri.path != '/api/containers') return '[]';
          return jsonEncode([
            {
              ..._container('aaa', 'stale-web', 'nginx'),
              'serverId': 'gone',
              'status': 'Up 2 hours',
            },
            {
              ..._container('bbb', 'sick-api', 'api'),
              'serverId': 'live',
              'status': 'Up 5 minutes (unhealthy)',
            },
          ]);
        }),
      );
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(home: ContainerListScreen(apiClient: client)),
      );
      await tester.pumpAndSettle();

      expect(find.text('Unknown'), findsOneWidget);
      expect(find.text('Was running · 3 d ago'), findsOneWidget);
      expect(
        find.text('1 container is on a disconnected server'),
        findsOneWidget,
      );
      expect(find.text('Unhealthy'), findsOneWidget);
      expect(find.text('Up 5 minutes (unhealthy)'), findsOneWidget);
      // Both rows show their actions, but only the reachable one's work.
      final stops = tester
          .widgetList<IconButton>(
            find.ancestor(
              of: find.byIcon(Icons.stop_rounded),
              matching: find.byType(IconButton),
            ),
          )
          .toList();
      expect(stops, hasLength(2));
      expect(stops.where((b) => b.onPressed != null), hasLength(1));
    },
  );
}

class _FakeClient extends http.BaseClient {
  final String Function(Uri) respond;
  _FakeClient(this.respond);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = utf8.encode(respond(request.url));
    return http.StreamedResponse(Stream.value(body), 200);
  }
}
