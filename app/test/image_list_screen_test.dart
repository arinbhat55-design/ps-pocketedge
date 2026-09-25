import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/widgets/state_message.dart';
import 'package:app/features/images/image_list_screen.dart';

void main() {
  testWidgets('image list screen shows empty state when no images exist', (
    WidgetTester tester,
  ) async {
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeJsonClient({'/api/images': '[]'}),
    );

    await tester.pumpWidget(
      MaterialApp(home: ImageListScreen(apiClient: client, isAdmin: false)),
    );
    await tester.pumpAndSettle();

    expect(find.text('No images yet'), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(StateMessage),
        matching: find.widgetWithText(FilledButton, 'Pull image'),
      ),
      findsOneWidget,
    );
    // Non-admin users shouldn't see the Registries/Policy tabs.
    expect(find.text('Registries'), findsNothing);
    expect(find.text('Policy'), findsNothing);
  });

  testWidgets('admin sees Images/Registries/Policy tabs', (
    WidgetTester tester,
  ) async {
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeJsonClient({
        '/api/images': '[]',
        '/api/registries': '[]',
        '/api/image-policies': '[]',
        '/api/image-policies/settings': '{"enabled":false}',
      }),
    );

    await tester.pumpWidget(
      MaterialApp(home: ImageListScreen(apiClient: client, isAdmin: true)),
    );
    await tester.pumpAndSettle();

    expect(find.text('Images'), findsWidgets);
    expect(find.text('Registries'), findsOneWidget);
    expect(find.text('Policy'), findsOneWidget);
  });

  testWidgets('renders a returned image with its tag and dangling badge', (
    WidgetTester tester,
  ) async {
    final images = jsonEncode([
      {
        'serverId': 's1',
        'serverName': 'my-server',
        'id': 'sha256:abc123',
        'repoTags': ['nginx:latest'],
        'repoDigests': ['nginx@sha256:abc123'],
        'sizeBytes': 1024 * 1024,
        'createdUnix': 1700000000,
        'dangling': false,
        'containersCount': 0,
      },
      {
        'serverId': 's1',
        'serverName': 'my-server',
        'id': 'sha256:def456',
        'repoTags': <String>[],
        'repoDigests': <String>[],
        'sizeBytes': 500,
        'createdUnix': 1700000001,
        'dangling': true,
        'containersCount': 0,
      },
    ]);
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeJsonClient({'/api/images': images}),
    );

    await tester.pumpWidget(
      MaterialApp(home: ImageListScreen(apiClient: client, isAdmin: false)),
    );
    await tester.pumpAndSettle();

    expect(find.text('nginx:latest'), findsOneWidget);
    expect(find.text('Untagged image'), findsOneWidget);
    expect(find.text('Dangling'), findsOneWidget);
    // A "my-server" chip for filtering plus its appearance in each row's
    // subtitle.
    expect(find.text('my-server'), findsWidgets);
  });
}

/// Fakes the control plane's JSON GET endpoints keyed by path, so these
/// widget tests don't depend on a running control plane. Any path not in
/// [responses] gets an empty JSON array — good enough for endpoints the
/// screen calls but the test doesn't care about (e.g. rollback history
/// probes triggered from elsewhere).
class _FakeJsonClient extends http.BaseClient {
  final Map<String, String> responses;
  _FakeJsonClient(this.responses);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = responses[request.url.path] ?? '[]';
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}
