import React, { useState, useEffect, useCallback, useRef } from 'react';
import { BottomNav } from './components';
import { api, on } from './bindings';
import { SplashScreen, WelcomeScreen, InvitePreviewScreen,
         RegistrationScreen, PasteUrlScreen } from './screens-onboarding';
import { ContactsTab, CallsTab, InvitesTab, ProfileTab,
         ContactDetailScreen, AddContactScreen } from './screens-main';
import { OutgoingCallScreen, IncomingCallScreen, InCallScreen,
         SafetyNumberScreen } from './screens-call';
import { CreateInviteScreen, InviteCreatedScreen, MyContactScreen,
         SettingsScreen, ServerInfoScreen } from './screens-other';

export default function App() {
  const [route, setRoute] = useState('splash');
  const [params, setParams] = useState({});
  const [conn, setConn] = useState('connected');
  const [profile, setProfile] = useState(null);
  const [incomingCall, setIncomingCall] = useState(null);
  const [activeCallId, setActiveCallId] = useState(null);
  const activeCallIdRef = useRef(null);
  const setActive = (v) => { activeCallIdRef.current = v; setActiveCallId(v); };
  const lastActiveStateRef = useRef(null);

  // Bootstrap on mount
  useEffect(() => {
    api.bootstrap().then(res => {
      setProfile(res.profile || null);
      setRoute(res.hasIdentity ? 'main' : 'welcome');
    }).catch(() => {
      setRoute('welcome');
    });
  }, []);

  // Listen for incoming calls
  useEffect(() => {
    const unsub = on('incoming-call', (data) => {
      setIncomingCall(data);
      setRoute('incoming');
    });
    return unsub;
  }, []);

  // Listen for incoming call cancellation (caller hung up before answer)
  useEffect(() => {
    const unsub = on('incoming-call-cancelled', () => {
      setIncomingCall(null);
      setActive(null);
      setRoute('main');
    });
    return unsub;
  }, []);

  // Listen for connection status
  useEffect(() => {
    const unsub = on('connection-status', (status) => {
      setConn(status);
    });
    return unsub;
  }, []);

  // Listen for call-state. Помним последний active-эвент и переключаем
  // outgoing→incall через ref (не через state-route, чтобы не зависеть
  // от ре-подписки при смене route).
  useEffect(() => {
    const unsub = on('call-state', (data) => {
      if (data.state === 'active') {
        lastActiveStateRef.current = data.callId;
        if (activeCallIdRef.current && data.callId === activeCallIdRef.current) {
          setRoute(prev => (prev === 'outgoing' ? 'incall' : prev));
        }
      }
    });
    return unsub;
  }, []);

  // Listen for call-ended to clear activeCallId
  useEffect(() => {
    const unsub = on('call-ended', (data) => {
      setActive(null);
      lastActiveStateRef.current = null;
    });
    return unsub;
  }, []);

  const go = useCallback((r, p) => {
    if (p) setParams(prev => ({ ...prev, ...p }));
    if (r === 'main' || r.startsWith('main-')) {
      setRoute('main');
    } else {
      setRoute(r);
    }
  }, []);

  const withGo = useCallback((screenRoute, extra) => (p) => go(screenRoute, { ...p, ...extra }), [go]);

  const id = params.id || '';

  // Determine chrome
  const showBottomNav = route === 'main';
  const isDark = ['outgoing', 'incoming', 'incall'].includes(route);

  // --- onboarding flow with real data ---

  const handleInvitePasted = async (url) => {
    try {
      const preview = await api.previewInvite(url);
      go('preview', { inviteUrl: url, preview });
    } catch (e) {
      alert('Ошибка: ' + (e.message || e));
    }
  };

  const handleRegister = async (url, name) => {
    try {
      await api.register(url, name);
      const boot = await api.bootstrap();
      setProfile(boot.profile || null);
      go('main');
    } catch (e) {
      alert('Ошибка регистрации: ' + (e.message || e));
    }
  };

  // --- contacts ---

  const handleStartCall = async (contactId) => {
    if (activeCallIdRef.current) {
      alert('Звонок уже активен (' + activeCallIdRef.current + ')');
      return;
    }
    lastActiveStateRef.current = null;
    setActive('pending');
    try {
      const res = await api.startCall(contactId);
      setActive(res.callId);
      const already = lastActiveStateRef.current === res.callId;
      go(already ? 'incall' : 'outgoing', { id: contactId, callId: res.callId });
    } catch (e) {
      setActive(null);
      alert('Ошибка вызова: ' + (e.message || e));
    }
  };

  const handleAcceptCall = async () => {
    if (!incomingCall) return;
    try {
      await api.acceptCall(incomingCall.callId);
      setActive(incomingCall.callId);
      setIncomingCall(null);
      go('incall', { id: incomingCall.from.user_id, callId: incomingCall.callId });
    } catch (e) {
      alert('Ошибка: ' + (e.message || e));
    }
  };

  const handleDeclineCall = async () => {
    if (!incomingCall) return;
    try {
      await api.declineCall(incomingCall.callId);
    } catch (e) {
      console.error('[declineCall]', e);
    }
    setIncomingCall(null);
    setActive(null);
    go('main');
  };

  const handleHangUp = async () => {
    try { await api.hangUp(); } catch (e) {
      console.error('[hangUp]', e);
    }
    setActive(null);
    go('main');
  };

  const renderScreen = () => {
    switch (route) {
      case 'splash':
        return <SplashScreen />;

      case 'welcome':
        return <WelcomeScreen onPaste={() => go('paste')} />;

      case 'paste':
        return <PasteUrlScreen onContinue={handleInvitePasted} onBack={() => go('welcome')} />;

      case 'preview':
        return (
          <InvitePreviewScreen
            preview={params.preview}
            onAccept={() => go('register', { inviteUrl: params.inviteUrl })}
            onBack={() => go('paste')}
          />
        );

      case 'register':
        return (
          <RegistrationScreen
            inviteUrl={params.inviteUrl}
            onRegister={handleRegister}
            onBack={() => go('preview', { inviteUrl: params.inviteUrl })}
          />
        );

      case 'main':
        return (
          <>
            <ContactsTab go={go} conn={conn} onCall={handleStartCall} activeCallId={activeCallId}
              style={{ display: (params.tab === 'contacts' || !params.tab) ? 'flex' : 'none', flexDirection: 'column', flex: 1 }} />
            <CallsTab go={go} onCall={handleStartCall} activeCallId={activeCallId}
              style={{ display: params.tab === 'calls' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }} />
            <InvitesTab go={go}
              style={{ display: params.tab === 'invites' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }} />
            <ProfileTab go={go} profile={profile}
              style={{ display: params.tab === 'profile' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }} />
          </>
        );

      case 'contact':
        return <ContactDetailScreen go={go} contactId={id} onCall={handleStartCall} activeCallId={activeCallId} />;
      case 'add-contact':
        return <AddContactScreen go={go} onBack={() => go('main')} />;
      case 'safety':
        return <SafetyNumberScreen go={go} contactId={id} />;
      case 'outgoing':
        return <OutgoingCallScreen go={go} contactId={id} onHangUp={handleHangUp} />;
      case 'incoming':
        return (
          <IncomingCallScreen
            incoming={incomingCall}
            onAccept={handleAcceptCall}
            onDecline={handleDeclineCall}
          />
        );
      case 'incall':
        return <InCallScreen go={go} contactId={id} callId={params.callId} onHangUp={handleHangUp} />;
      case 'create-invite':
        return <CreateInviteScreen go={go} />;
      case 'invite-created':
        return <InviteCreatedScreen go={go} inviteUrl={params.inviteUrl} />;
      case 'my-contact':
        return <MyContactScreen go={go} />;
      case 'settings':
        return <SettingsScreen go={go} />;
      case 'server-info':
        return <ServerInfoScreen go={go} />;
      default:
        return <SplashScreen />;
    }
  };

  return (
    <div className="app-shell" style={{ height: '100vh' }}>
      <div className={"app-content" + (isDark ? " dark" : "")}>
        {renderScreen()}
      </div>
      {showBottomNav && <BottomNav active={params.tab || 'contacts'} onChange={tab => {
        if (tab === 'settings') go('settings');
        else go('main', { tab });
      }} />}
    </div>
  );
}
