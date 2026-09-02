import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:app/features/containers/resource_limits_dialog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  testWidgets(
    'converts cores/MB to nanoCPUs/bytes and PATCHes the resources endpoint',
    (tester) async {
      final capturingClient = _CapturingClient();
      final apiClient = ApiClient(
        baseUrl: 'http://localhost:8080',
        httpClient: capturingClient,
      );

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Builder(
              builder: (context) => ElevatedButton(
                onPressed: () => showResourceLimitsDialog(
                  context,
                  apiClient: apiClient,
                  serverId: 'server-1',
                  containerId: 'container-1',
                  currentNanoCpus: 0,
                  currentMemoryLimitBytes: 0,
                  currentMemoryReservationBytes: 0,
                  currentPidsLimit: 0,
                ),
                child: const Text('Open'),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      await tester.enterText(
        find.widgetWithText(
          TextField,
          'CPU limit, in cores (blank = unlimited)',
        ),
        '0.5',
      );
      await tester.enterText(
        find.widgetWithText(
          TextField,
          'Memory limit, in MB (blank = unlimited)',
        ),
        '256',
      );

      await tester.tap(find.widgetWithText(FilledButton, 'Save'));
      await tester.pumpAndSettle();

      expect(capturingClient.lastRequest, isNotNull);
      final request = capturingClient.lastRequest!;
      expect(request.method, 'PATCH');
      expect(
        request.url.path,
        '/api/servers/server-1/containers/container-1/resources',
      );
      final body = jsonDecode(request.body) as Map<String, dynamic>;
      expect(body['nanoCpus'], 500000000);
      expect(body['memoryLimitBytes'], 256 * 1024 * 1024);
      expect(body['memoryReservationBytes'], 0);
      expect(body['pidsLimit'], 0);

      // Dialog closes on success.
      expect(find.text('Resource limits'), findsNothing);
    },
  );

  testWidgets('shows the server-reported error and keeps the dialog open', (
    tester,
  ) async {
    final apiClient = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FixedResponseClient(
        jsonEncode({'success': false, 'error': 'container not found'}),
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => ElevatedButton(
              onPressed: () => showResourceLimitsDialog(
                context,
                apiClient: apiClient,
                serverId: 'server-1',
                containerId: 'container-1',
                currentNanoCpus: 0,
                currentMemoryLimitBytes: 0,
                currentMemoryReservationBytes: 0,
                currentPidsLimit: 0,
              ),
              child: const Text('Open'),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(find.text('container not found'), findsOneWidget);
    expect(find.text('Resource limits'), findsOneWidget);
  });
}

/// Records the last request it received and replies with a generic
/// success — enough for [showResourceLimitsDialog] to treat the call as
/// having succeeded.
class _CapturingClient extends http.BaseClient {
  http.Request? lastRequest;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request is http.Request) lastRequest = request;
    final body = utf8.encode(jsonEncode({'success': true}));
    return http.StreamedResponse(Stream.value(body), 200);
  }
}

/// Always replies with the same canned body, regardless of the request —
/// for asserting on how the widget reacts to a given server response.
class _FixedResponseClient extends http.BaseClient {
  final String responseBody;
  _FixedResponseClient(this.responseBody);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = utf8.encode(responseBody);
    return http.StreamedResponse(Stream.value(body), 200);
  }
}
