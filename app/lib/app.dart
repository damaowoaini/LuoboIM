import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import 'core/app_runtime.dart';
import 'core/config/app_config.dart';
import 'design/components/brand_mark.dart';
import 'design/tokens.dart';
import 'features/auth/presentation/auth_page.dart';
import 'features/app_lock/presentation/app_access_host.dart';
import 'features/app_lock/presentation/app_lock_settings_page.dart';
import 'features/chat/presentation/chat_page.dart';
import 'features/conversation/presentation/conversations_page.dart';
import 'features/search/domain/global_search.dart';
import 'features/search/presentation/global_search_page.dart';
import 'features/contacts/domain/contact.dart' show ContactRequestKind;
import 'features/contacts/presentation/contacts_page.dart';
import 'features/contacts/presentation/contact_requests_page.dart';
import 'features/contacts/presentation/contact_search_page.dart';
import 'features/contacts/presentation/user_profile_page.dart';
import 'features/group/presentation/group_page.dart';
import 'features/group/presentation/group_members_page.dart';
import 'features/mine/presentation/mine_page.dart';
import 'features/mine/presentation/account_profile_page.dart';
import 'features/mine/presentation/account_settings_page.dart';
import 'features/mine/presentation/change_password_page.dart';
import 'features/mine/presentation/cancel_account_page.dart';
import 'features/complaint/presentation/my_complaints_page.dart';
import 'features/moments/presentation/moments_page.dart';
import 'features/moments/presentation/moment_publish_page.dart';
import 'features/moments/presentation/moment_interactions_page.dart';
import 'features/meeting/presentation/meetings_page.dart';
import 'features/meeting/booking/presentation/bookings_page.dart';
import 'features/meeting/booking/presentation/booking_editor_page.dart';
import 'features/meeting/booking/presentation/booking_detail_page.dart';
import 'features/personal/domain/personal_item.dart';
import 'features/personal/presentation/personal_page.dart';
import 'features/favorite/presentation/favorites_page.dart';
import 'features/favorite/presentation/favorite_detail_page.dart';
import 'features/favorite/presentation/favorite_categories_page.dart';
import 'features/favorite/presentation/favorite_stats_page.dart';
import 'features/rtc/presentation/rtc_page.dart';
import 'providers.dart';

final routerProvider = Provider<GoRouter>((ref) {
  // Local locking retires modal/navigation state as well as data leases. A
  // fresh explicit action after unlock must not reuse an old editor's save.
  ref.watch(appAccessProvider.select((access) => access.revision));
  final runtime = ref.watch(appRuntimeProvider.notifier);
  final router = GoRouter(
    initialLocation: '/startup',
    refreshListenable: runtime,
    redirect: (context, state) {
      final path = state.uri.path;
      return switch (runtime.phase) {
        AppPhase.checkingConsent ||
        AppPhase.starting => path == '/startup' ? null : '/startup',
        AppPhase.consent => path == '/privacy' ? null : '/privacy',
        AppPhase.unavailable => path == '/unavailable' ? null : '/unavailable',
        AppPhase.login || AppPhase.signingIn =>
          ['/login', '/register', '/reset'].contains(path) ? null : '/login',
        AppPhase.online =>
          [
                    '/home',
                    '/mine',
                    '/personal/notes',
                    '/personal/todos',
                    '/meetings',
                  ].contains(path) ||
                  path.startsWith('/chat/') ||
                  path.startsWith('/mine/') ||
                  path == '/meetings/bookings' ||
                  path == '/meetings/bookings/new' ||
                  RegExp(r'^/meetings/bookings/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$').hasMatch(path) ||
                  path == '/contacts' ||
                  path.startsWith('/contacts/') ||
                  path == '/favorites' ||
                  path.startsWith('/favorites/') ||
                  path == '/moments' ||
                  path.startsWith('/moments/') ||
                  path == '/rtc'
              ? null
              : '/home',
      };
    },
    routes: [
      GoRoute(path: '/startup', builder: (_, _) => const StartupPage()),
      GoRoute(path: '/privacy', builder: (_, _) => const PrivacyPage()),
      GoRoute(
        path: '/unavailable',
        builder: (_, _) => const ServiceUnavailablePage(),
      ),
      GoRoute(path: '/login', builder: (_, _) => const AuthPage()),
      GoRoute(
        path: '/register',
        builder: (_, _) => const AuthPage(mode: AuthPageMode.register),
      ),
      GoRoute(
        path: '/reset',
        builder: (_, _) => const AuthPage(mode: AuthPageMode.reset),
      ),
      ShellRoute(
        builder: (context, state, child) =>
            MainShell(path: state.uri.path, child: child),
        routes: [
          GoRoute(path: '/home', builder: (_, _) => const ConversationsPage()),
          GoRoute(path: '/contacts', builder: (_, _) => const ContactsPage()),
          GoRoute(path: '/mine', builder: (_, _) => const MinePage()),
        ],
      ),
      GoRoute(path: '/rtc', builder: (_, _) => const RtcPage()),
      GoRoute(path: '/meetings', builder: (_, _) => const MeetingsPage()),
      GoRoute(path: '/meetings/bookings', builder: (_, _) => const BookingsPage()),
      GoRoute(path: '/meetings/bookings/new', builder: (_, _) => const BookingEditorPage()),
      GoRoute(path: '/meetings/bookings/:bookingID', builder: (_, state) => BookingDetailPage(bookingID: state.pathParameters['bookingID']!)),
      GoRoute(
        path: '/mine/password',
        builder: (_, _) => const ChangePasswordPage(),
      ),
      GoRoute(
        path: '/mine/cancel',
        builder: (_, _) => const CancelAccountPage(),
      ),
      GoRoute(
        path: '/mine/complaints',
        builder: (_, _) => const MyComplaintsPage(),
      ),
      GoRoute(
        path: '/mine/profile',
        builder: (_, _) => const AccountProfilePage(),
      ),
      GoRoute(
        path: '/mine/settings',
        builder: (_, _) => const AccountSettingsPage(),
      ),
      GoRoute(
        path: '/mine/app-lock',
        builder: (_, _) => const AppLockSettingsPage(),
      ),
      GoRoute(path: '/moments', builder: (_, _) => const MomentsPage()),
      GoRoute(
        path: '/moments/publish',
        builder: (_, _) => const MomentPublishPage(),
      ),
      GoRoute(
        path: '/moments/interactions',
        builder: (_, _) => const MomentInteractionsPage(),
      ),
      GoRoute(
        path: '/moments/user/:userId',
        builder: (_, state) =>
            MomentsPage(author: state.pathParameters['userId']!),
      ),
      GoRoute(
        path: '/moments/item/:momentId',
        builder: (_, state) =>
            MomentDetailPage(id: state.pathParameters['momentId']!),
      ),
      GoRoute(path: '/favorites', builder: (_, _) => const FavoritesPage()),
      GoRoute(
        path: '/favorites/stats',
        builder: (_, _) => const FavoriteStatsPage(),
      ),
      GoRoute(
        path: '/favorites/categories',
        builder: (_, _) => const FavoriteCategoriesPage(),
      ),
      GoRoute(
        path: '/favorites/item/:favoriteId',
        builder: (_, state) =>
            FavoriteDetailPage(favoriteId: state.pathParameters['favoriteId']!),
      ),
      GoRoute(path: '/search', builder: (_, _) => const GlobalSearchPage()),
      GoRoute(
        path: '/contacts/search',
        builder: (_, _) => const ContactSearchPage(),
      ),
      GoRoute(
        path: '/contacts/requests/:kind',
        builder: (_, state) => ContactRequestsPage(
          kind: state.pathParameters['kind'] == 'group'
              ? ContactRequestKind.group
              : ContactRequestKind.friend,
        ),
      ),
      GoRoute(
        path: '/contacts/blocked',
        builder: (_, _) => const ContactCollectionPage(),
      ),
      GoRoute(
        path: '/contacts/groups',
        builder: (_, _) => const ContactCollectionPage(groups: true),
      ),
      GoRoute(
        path: '/contacts/groups/:groupId',
        builder: (_, state) =>
            GroupPage(groupId: state.pathParameters['groupId']!),
      ),
      GoRoute(
        path: '/contacts/groups/:groupId/members',
        builder: (_, state) =>
            GroupMembersPage(groupId: state.pathParameters['groupId']!),
      ),
      GoRoute(
        path: '/contacts/user/:userId',
        builder: (_, state) => UserProfilePage(
          userId: state.pathParameters['userId']!,
          groupId: state.uri.queryParameters['group'],
        ),
      ),
      GoRoute(
        path: '/personal/notes',
        builder: (_, _) => const PersonalPage(kind: PersonalKind.note),
      ),
      GoRoute(
        path: '/personal/todos',
        builder: (_, _) => const PersonalPage(kind: PersonalKind.todo),
      ),
      GoRoute(
        path: '/chat/:conversationId',
        builder: (_, state) {
          final cid = state.pathParameters['conversationId']!;
          final id = state.uri.queryParameters['message'];
          return ChatPage(
            conversationId: cid,
            sourceMessageId: id,
            sourceCurrent: globalSourceGuard(cid, id, state.extra,
                fromGlobal: state.uri.queryParameters['source'] == 'global'),
          );
        },
      ),
    ],
  );
  ref.onDispose(router.dispose);
  return router;
});

class MainShell extends StatelessWidget {
  const MainShell({super.key, required this.path, required this.child});
  final String path;
  final Widget child;
  @override
  Widget build(BuildContext context) => Scaffold(
    body: child,
    bottomNavigationBar: NavigationBar(
      selectedIndex: path == '/mine'
          ? 2
          : path == '/contacts'
          ? 1
          : 0,
      onDestinationSelected: (index) =>
          context.go(['/home', '/contacts', '/mine'][index]),
      destinations: const [
        NavigationDestination(
          icon: Icon(Icons.forum_outlined),
          selectedIcon: Icon(Icons.forum),
          label: '消息',
        ),
        NavigationDestination(
          icon: Icon(Icons.contacts_outlined),
          selectedIcon: Icon(Icons.contacts),
          label: '通讯录',
        ),
        NavigationDestination(
          icon: Icon(Icons.person_outline),
          selectedIcon: Icon(Icons.person),
          label: '我的',
        ),
      ],
    ),
  );
}

class LuoboApp extends ConsumerWidget {
  const LuoboApp({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final router = ref.watch(routerProvider);
    return MaterialApp.router(
      title: Brand.name,
      debugShowCheckedModeBanner: false,
      theme: buildAppTheme(),
      locale: const Locale('zh', 'CN'),
      supportedLocales: const [Locale('zh', 'CN'), Locale('en')],
      localizationsDelegates: GlobalMaterialLocalizations.delegates,
      routerConfig: router,
      builder: (context, child) => AppAccessHost(
        child: _RtcNavigationHost(
          router: router,
          child: child ?? const SizedBox.shrink(),
        ),
      ),
    );
  }
}

class _RtcNavigationHost extends StatefulWidget {
  const _RtcNavigationHost({required this.router, required this.child});
  final GoRouter router;
  final Widget child;
  @override
  State<_RtcNavigationHost> createState() => _RtcNavigationHostState();
}

class _RtcNavigationHostState extends State<_RtcNavigationHost> {
  bool pending = false;
  bool get inCallPage =>
      widget.router.routerDelegate.currentConfiguration.uri.path == '/rtc';
  late bool showingCallPage;

  @override
  void initState() {
    super.initState();
    showingCallPage = inCallPage;
    widget.router.routerDelegate.addListener(routeChanged);
  }

  @override
  void didUpdateWidget(covariant _RtcNavigationHost oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.router != widget.router) {
      oldWidget.router.routerDelegate.removeListener(routeChanged);
      widget.router.routerDelegate.addListener(routeChanged);
      showingCallPage = inCallPage;
    }
  }

  void routeChanged() {
    if (pending || showingCallPage == inCallPage) return;
    pending = true;
    // GoRouter can resolve the initial redirect while Router is building.
    // Update the overlay after that frame rather than rebuilding an ancestor.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      pending = false;
      if (mounted && showingCallPage != inCallPage) {
        setState(() => showingCallPage = inCallPage);
      }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  @override
  void dispose() {
    widget.router.routerDelegate.removeListener(routeChanged);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => RtcIncomingHost(
    showOngoingBar: !showingCallPage,
    onOpenCall: () {
      if (!inCallPage) widget.router.push('/rtc');
    },
    child: widget.child,
  );
}

class StartupPage extends StatelessWidget {
  const StartupPage({super.key});
  @override
  Widget build(BuildContext context) => const Scaffold(
    body: SafeArea(
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            BrandMark(size: 76),
            SizedBox(height: 20),
            Text(
              Brand.name,
              style: TextStyle(fontSize: 26, fontWeight: FontWeight.w700),
            ),
            SizedBox(height: 28),
            CircularProgressIndicator(),
            SizedBox(height: 16),
            Text('正在连接', style: TextStyle(color: AppColors.secondary)),
          ],
        ),
      ),
    ),
  );
}

class PrivacyPage extends ConsumerWidget {
  const PrivacyPage({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final runtime = ref.watch(appRuntimeProvider);
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 460),
            child: SingleChildScrollView(
              padding: const EdgeInsets.all(28),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const BrandMark(size: 76),
                  const SizedBox(height: 28),
                  const Text(
                    '欢迎使用萝卜对话',
                    style: TextStyle(fontSize: 28, fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 20),
                  const Text(
                    '开始前，请阅读用户协议与隐私政策，了解消息、好友关系和音视频通信所需的信息及权限。',
                    style: TextStyle(fontSize: 16, height: 1.7),
                  ),
                  const SizedBox(height: 12),
                  const Text(
                    '你可以自行决定是否继续。相机、麦克风和相册权限会在使用相关功能时申请。',
                    style: TextStyle(color: AppColors.secondary, height: 1.7),
                  ),
                  const SizedBox(height: 20),
                  Wrap(
                    spacing: 8,
                    children: [
                      TextButton(
                        onPressed: () =>
                            _open(context, runtime.config.termsUrl),
                        child: const Text('用户协议'),
                      ),
                      TextButton(
                        onPressed: () =>
                            _open(context, runtime.config.privacyUrl),
                        child: const Text('隐私政策'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 28),
                  if (!runtime.config.hasPolicies)
                    const Padding(
                      padding: EdgeInsets.only(bottom: 16),
                      child: Text(
                        '服务暂不可用，请稍后重试',
                        style: TextStyle(color: AppColors.error),
                      ),
                    ),
                  if (runtime.failure != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 16),
                      child: Text(
                        runtime.failure!.message,
                        style: const TextStyle(color: AppColors.error),
                      ),
                    ),
                  SizedBox(
                    width: double.infinity,
                    child: FilledButton(
                      onPressed: runtime.config.hasPolicies
                          ? runtime.acceptConsent
                          : null,
                      child: const Text('同意并继续'),
                    ),
                  ),
                  const SizedBox(height: 10),
                  const Text(
                    '点击“同意并继续”，表示你已阅读并同意上述协议。',
                    style: TextStyle(color: AppColors.secondary, fontSize: 13),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context, String raw) async {
    final uri = Uri.tryParse(raw);
    var opened = false;
    if (uri != null &&
        uri.scheme == 'https' &&
        uri.host.isNotEmpty &&
        uri.userInfo.isEmpty) {
      try {
        opened = await launchUrl(uri, mode: LaunchMode.externalApplication);
      } catch (_) {}
    }
    if (!opened && context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('暂时无法打开，请稍后重试')));
    }
  }
}

class ServiceUnavailablePage extends ConsumerWidget {
  const ServiceUnavailablePage({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) => Scaffold(
    body: SafeArea(
      child: Center(
        child: Padding(
          padding: const EdgeInsets.all(28),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const BrandMark(),
              const SizedBox(height: 24),
              const Text(
                '暂时无法连接',
                style: TextStyle(fontSize: 24, fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 12),
              const Text(
                '请检查网络，或稍后再试。',
                style: TextStyle(color: AppColors.secondary),
              ),
              const SizedBox(height: 24),
              FilledButton.icon(
                onPressed: ref.read(appRuntimeProvider).retry,
                icon: const Icon(Icons.refresh),
                label: const Text('重新连接'),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}
