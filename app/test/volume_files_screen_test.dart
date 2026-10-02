import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:app/features/volumes/volume_files_screen.dart';
import 'package:app/models/volume.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

class _FilesClient extends http.BaseClient {
  final requests = <http.Request>[];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final req = request as http.Request;
    requests.add(req);
    final path = req.url.queryParameters['path'] ?? '';
    final content = req.url.path.endsWith('/content');
    final body = content
        ? 'hello'
        : jsonEncode(
            path == 'notes'
                ? [
                    {'name': 'hello.txt', 'sizeBytes': 5},
                  ]
                : [
                    {'name': 'notes', 'isDirectory': true},
                  ],
          );
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}

void main() {
  testWidgets('volume browser opens a directory and reads a file', (
    tester,
  ) async {
    final fake = _FilesClient();
    final api = ApiClient(baseUrl: 'http://localhost:8080', httpClient: fake);
    const volume = VolumeSummary(
      serverId: 'server-1',
      serverName: 'local',
      name: 'data',
      driver: 'local',
      mountpoint: '/var/lib/docker/volumes/data',
    );
    await tester.pumpWidget(
      MaterialApp(
        home: VolumeFilesScreen(apiClient: api, volume: volume, isAdmin: false),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('notes'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('hello.txt'));
    await tester.pumpAndSettle();

    expect(find.text('hello'), findsOneWidget);
    expect(
      fake.requests.map((r) => r.url.queryParameters['path']),
      containsAll(['', 'notes', 'notes/hello.txt']),
    );
    expect(find.byTooltip('Upload file (1 MiB max)'), findsNothing);
  });
}
