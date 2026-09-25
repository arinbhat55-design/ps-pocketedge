import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/servers/server_list_screen.dart';
import 'package:app/models/server.dart';
import 'package:app/theme/app_theme.dart';
import 'package:app/widgets/metric_card.dart';
import 'package:app/widgets/status_pill.dart';

Server _server({String status = 'online', DateTime? heartbeat}) => Server(
  id: 's1',
  name: 'edge-1',
  hostname: 'edge-1.local',
  os: 'linux',
  arch: 'arm64',
  agentVersion: '1.0.0',
  status: status,
  lastHeartbeatAt: heartbeat,
  lastResources: null,
  createdAt: DateTime(2026),
);

void main() {
  final now = DateTime(2026, 9, 25, 12);

  group('serverStatus', () {
    test('recent heartbeat is Online', () {
      final s = serverStatus(
        _server(heartbeat: now.subtract(const Duration(seconds: 20))),
        now: now,
      );
      expect(s.label, 'Online');
      expect(s.tone, StatusTone.healthy);
    });

    test(
      'a stale heartbeat reads as Disconnected even if status is online',
      () {
        final s = serverStatus(
          _server(heartbeat: now.subtract(const Duration(minutes: 10))),
          now: now,
        );
        expect(s.label, 'Disconnected');
        expect(s.tone, StatusTone.failed);
      },
    );

    test('never reported is neutral, not failed', () {
      final s = serverStatus(_server(), now: now);
      expect(s.tone, StatusTone.neutral);
    });
  });

  test('containerStatus pairs every state with words', () {
    expect(containerStatus('running').label, 'Running');
    expect(containerStatus('exited').label, 'Stopped');
    expect(containerStatus('exited').tone, StatusTone.neutral);
    expect(containerStatus('dead').tone, StatusTone.failed);
  });

  testWidgets('MetricCard shows the value, range and a stale state', (
    tester,
  ) async {
    Future<void> pump({required bool stale}) => tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: Scaffold(
          body: MetricCard(
            label: 'CPU',
            icon: Icons.memory,
            points: [
              MetricPoint(now.subtract(const Duration(minutes: 10)), 20),
              MetricPoint(now.subtract(const Duration(minutes: 1)), 42),
            ],
            range: const Duration(hours: 1),
            now: now,
            stale: stale,
          ),
        ),
      ),
    );

    await pump(stale: false);
    expect(find.text('42'), findsOneWidget);
    expect(find.text('1 h ago'), findsOneWidget);
    expect(find.textContaining('peak 42%'), findsOneWidget);

    await pump(stale: true);
    expect(find.text('Stale'), findsOneWidget);
    expect(find.textContaining('Last known value'), findsOneWidget);
  });

  testWidgets('server list summarises online and disconnected counts', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    final fresh = DateTime.now().toUtc().toIso8601String();
    final old = DateTime.now()
        .subtract(const Duration(hours: 1))
        .toUtc()
        .toIso8601String();
    Map<String, dynamic> server(String id, String heartbeat) => {
      'id': id,
      'name': id,
      'hostname': '$id.local',
      'os': 'linux',
      'arch': 'amd64',
      'agentVersion': '1.0.0',
      'status': 'online',
      'lastHeartbeatAt': heartbeat,
      'lastResources': {'cpuPercent': 12, 'memPercent': 80, 'diskPercent': 95},
      'createdAt': fresh,
    };
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeClient(
        jsonEncode([server('a', fresh), server('b', fresh), server('c', old)]),
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: ServerListScreen(apiClient: client),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('online'), findsOneWidget);
    expect(find.text('2'), findsOneWidget);
    expect(find.text('Disconnected'), findsOneWidget);
    expect(find.text('Online'), findsNWidgets(2));
    // Add server lives in the header on desktop.
    expect(
      find.descendant(
        of: find.byType(AppBar),
        matching: find.text('Add server'),
      ),
      findsOneWidget,
    );
  });
}

class _FakeClient extends http.BaseClient {
  final String body;
  _FakeClient(this.body);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}
