import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:app/features/volumes/volume_delete_dialog.dart';
import 'package:app/models/volume.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

const _volume = VolumeSummary(
  serverId: 'server-1',
  serverName: 'prod-1',
  name: 'data-vol',
  driver: 'local',
  mountpoint: '/var/lib/docker/volumes/data-vol/_data',
);

Future<void> _openDialog(
  WidgetTester tester,
  ApiClient apiClient, {
  VolumeSummary volume = _volume,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: Builder(
          builder: (context) => ElevatedButton(
            onPressed: () => showVolumeDeleteDialog(
              context,
              apiClient: apiClient,
              volume: volume,
            ),
            child: const Text('Open'),
          ),
        ),
      ),
    ),
  );
  await tester.tap(find.text('Open'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('delete button stays disabled until the volume name matches', (
    tester,
  ) async {
    final client = _CapturingClient();
    final apiClient = ApiClient(baseUrl: 'http://localhost:8080', httpClient: client);

    await _openDialog(tester, apiClient);

    FilledButton deleteButton() =>
        tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Delete'));

    expect(deleteButton().onPressed, isNull);

    await tester.enterText(find.byType(TextField), 'wrong-name');
    await tester.pump();
    expect(deleteButton().onPressed, isNull);

    await tester.enterText(find.byType(TextField), 'data-vol');
    await tester.pump();
    expect(deleteButton().onPressed, isNotNull);
  });

  testWidgets(
    'submitting with the matching name DELETEs without force and closes on success',
    (tester) async {
      final client = _CapturingClient();
      final apiClient = ApiClient(baseUrl: 'http://localhost:8080', httpClient: client);

      await _openDialog(tester, apiClient);
      await tester.enterText(find.byType(TextField), 'data-vol');
      await tester.pump();
      await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
      await tester.pumpAndSettle();

      expect(client.lastRequest, isNotNull);
      expect(client.lastRequest!.method, 'DELETE');
      expect(
        client.lastRequest!.url.path,
        '/api/servers/server-1/volumes/data-vol',
      );
      expect(client.lastRequest!.url.queryParameters['force'], isNull);
      expect(find.text('Delete volume?'), findsNothing);
    },
  );

  testWidgets(
    'an in-use failure offers force delete, which retries with force=true and succeeds',
    (tester) async {
      final client = _ForceAwareClient(blockReason: 'volume "data-vol" is in use by container(s) [c1]');
      final apiClient = ApiClient(baseUrl: 'http://localhost:8080', httpClient: client);

      await _openDialog(tester, apiClient);
      await tester.enterText(find.byType(TextField), 'data-vol');
      await tester.pump();
      await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
      await tester.pumpAndSettle();

      // Blocked: error shown, force option offered instead of "Delete".
      expect(find.textContaining('in use'), findsWidgets);
      expect(find.widgetWithText(FilledButton, 'Force delete'), findsOneWidget);
      expect(find.widgetWithText(FilledButton, 'Delete'), findsNothing);

      await tester.tap(find.widgetWithText(FilledButton, 'Force delete'));
      await tester.pumpAndSettle();

      expect(client.lastRequest!.url.queryParameters['force'], 'true');
      expect(find.text('Delete volume?'), findsNothing);
    },
  );

  testWidgets(
    'a failure unrelated to "in use" does not offer a force-delete retry',
    (tester) async {
      final apiClient = ApiClient(
        baseUrl: 'http://localhost:8080',
        httpClient: _FixedResponseClient(
          jsonEncode({'success': false, 'error': 'permission denied'}),
        ),
      );

      await _openDialog(tester, apiClient);
      await tester.enterText(find.byType(TextField), 'data-vol');
      await tester.pump();
      await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
      await tester.pumpAndSettle();

      expect(find.text('permission denied'), findsOneWidget);
      expect(find.widgetWithText(FilledButton, 'Force delete'), findsNothing);
      // The primary button reverts to disabled (name field unchanged, but
      // the enabled-without-retyping exception only applies once force is
      // actually offered).
      expect(find.text('Delete volume?'), findsOneWidget);
    },
  );
}

/// Records the last request it received and replies with a generic
/// success.
class _CapturingClient extends http.BaseClient {
  http.BaseRequest? lastRequest;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    lastRequest = request;
    final body = utf8.encode(jsonEncode({'success': true}));
    return http.StreamedResponse(Stream.value(body), 200);
  }
}

/// Fails every request unless the URL carries `force=true`, at which point
/// it succeeds — models the agent's in-use guard (RemoveVolume) plus a
/// successful forced retry.
class _ForceAwareClient extends http.BaseClient {
  final String blockReason;
  http.BaseRequest? lastRequest;

  _ForceAwareClient({required this.blockReason});

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    lastRequest = request;
    final forced = request.url.queryParameters['force'] == 'true';
    final body = utf8.encode(
      jsonEncode(
        forced
            ? {'success': true}
            : {'success': false, 'error': blockReason},
      ),
    );
    return http.StreamedResponse(Stream.value(body), 200);
  }
}

/// Always replies with the same canned body, regardless of the request.
class _FixedResponseClient extends http.BaseClient {
  final String responseBody;
  _FixedResponseClient(this.responseBody);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = utf8.encode(responseBody);
    return http.StreamedResponse(Stream.value(body), 200);
  }
}
