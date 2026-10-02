import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/servers/server_detail_screen.dart';

void main() {
  Map<String, dynamic> detail(DateTime heartbeat) => {
    'id': 's1',
    'name': 'old-box',
    'hostname': 'old-box',
    'os': 'linux',
    'arch': 'amd64',
    'agentVersion': '1',
    'status': 'online',
    'lastHeartbeatAt': heartbeat.toUtc().toIso8601String(),
    'lastResources': null,
    'createdAt': heartbeat.toUtc().toIso8601String(),
    'containers': <Object>[],
  };

  Future<List<String>> pumpDetail(
    WidgetTester tester,
    DateTime heartbeat,
  ) async {
    final requests = <String>[];
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _Fake((req) {
        requests.add('${req.method} ${req.url.path}');
        if (req.method == 'DELETE') return (204, '');
        if (req.url.path == '/api/servers/s1') {
          return (200, jsonEncode(detail(heartbeat)));
        }
        return (200, '[]');
      }),
    );
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute<bool>(
                  builder: (_) => ServerDetailScreen(
                    apiClient: client,
                    serverId: 's1',
                    serverName: 'old-box',
                    isAdmin: true,
                  ),
                ),
              ),
              child: const Text('open'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pump(const Duration(seconds: 1));
    await tester.pump(const Duration(seconds: 1));
    return requests;
  }

  testWidgets('admin can remove a disconnected server', (tester) async {
    final requests = await pumpDetail(
      tester,
      DateTime.now().subtract(const Duration(days: 22)),
    );

    await tester.tap(find.byTooltip('More'));
    // Let the menu finish opening; it ignores taps while animating.
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 200));
    }
    await tester.tap(find.text('Remove server'));
    // Menu close animation, then onSelected opens the dialog.
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 200));
    }
    expect(find.text('Remove old-box?'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Remove server'));
    await tester.pump(const Duration(seconds: 1));
    await tester.pump(const Duration(seconds: 1));

    expect(requests, contains('DELETE /api/servers/s1'));
    expect(find.text('open'), findsOneWidget); // popped back
  });

  testWidgets('a connected server cannot be removed', (tester) async {
    await pumpDetail(tester, DateTime.now());

    await tester.tap(find.byTooltip('More'));
    // Let the menu finish opening; it ignores taps while animating.
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 200));
    }
    expect(
      find.text('Only disconnected servers can be removed'),
      findsOneWidget,
    );
    final item = tester.widget<PopupMenuItem<String>>(
      find.byType(PopupMenuItem<String>),
    );
    expect(item.enabled, isFalse);
  });
}

class _Fake extends http.BaseClient {
  final (int, String) Function(http.BaseRequest) respond;
  _Fake(this.respond);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final (code, body) = respond(request);
    return http.StreamedResponse(Stream.value(utf8.encode(body)), code);
  }
}
