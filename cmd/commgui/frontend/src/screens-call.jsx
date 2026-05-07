import React, { useState, useEffect, useRef } from 'react';
import Icon from './icons';
import { Avatar, AppBar } from './components';
import { api, on } from './bindings';

// ==============================================================================
// Call screens: Outgoing, Incoming, InCall, SafetyNumber
// ==============================================================================

const statusLines = {
  calling:        { text: 'Вызов…',           color: '#b9c5c0', pulse: true },
  connecting:     { text: 'Соединение…',      color: '#80cbc4', pulse: true },
  active:         { text: 'На связи',          color: '#3ddc84', pulse: false },
  timeout:        { text: 'Не отвечает',       color: '#ff8a65', pulse: false },
  declined:       { text: 'Занято',            color: '#ff8a65', pulse: false },
  failed:         { text: 'Ошибка вызова',     color: 'var(--md-error)', pulse: false },
  completed:      { text: 'Завершён',          color: '#b9c5c0', pulse: false },
};

export function OutgoingCallScreen({ go, contactId, onHangUp }) {
  const [t, setT] = useState(0);
  const [callState, setCallState] = useState('calling');
  const [ice, setIce] = useState('new');
  const [outcome, setOutcome] = useState('');
  const nameRef = useRef('');
  const startRef = useRef(Date.now());
  const endedRef = useRef(false);

  useEffect(() => {
    api.listContacts().then(list => {
      const c = list.find(x => x.user_id === contactId || x.alias === contactId);
      if (c) nameRef.current = c.alias || c.name;
    }).catch(() => {});
  }, [contactId]);

  useEffect(() => {
    const timer = setInterval(() => setT(Math.floor((Date.now() - startRef.current) / 1000)), 200);
    const unsub = on('call-state', (data) => {
      if (data.state === 'active') setCallState('active');
    });
    const unsubIce = on('call-ice', (data) => {
      setIce(data.iceState);
    });
    const unsubEnd = on('call-ended', (data) => {
      clearInterval(timer);
      endedRef.current = true;
      const oc = data.outcome || 'completed';
      setOutcome(oc);
      if (oc === 'answered') {
        // успешный звонок — переход в InCallScreen уже случился по call-state=active
        return;
      }
      // неуспешный — показываем результат и уходим
      setTimeout(() => go('main'), 2500);
    });
    return () => { clearInterval(timer); unsub(); unsubIce(); unsubEnd(); };
  }, []);

  const mm = String(Math.floor(t / 60)).padStart(2, '0');
  const ss = String(t % 60).padStart(2, '0');

  let displayState = callState;
  if (outcome && outcome !== 'answered') {
    displayState = outcome;
  } else if (callState === 'active') {
    displayState = ice === 'connected' ? 'active' : 'connecting';
  }
  const st = statusLines[displayState] || statusLines.calling;

  return (
    <div style={{ flex: 1, background: '#0e1614', display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '48px 24px 24px' }}>
      <div style={{ position: 'relative', width: 144, height: 144, marginTop: 32 }}>
        {st.pulse && (
          <>
            <div className="pulse-ring" />
            <div className="pulse-ring delay" />
          </>
        )}
        <Avatar name={nameRef.current} size="xl" />
      </div>
      <div style={{ fontSize: 32, fontWeight: 400, color: '#fff', marginTop: 32 }}>{nameRef.current}</div>
      <div style={{ fontSize: 14, color: st.color, marginTop: 8, display: 'flex', alignItems: 'center', gap: 6 }}>
        {st.pulse && <Icon name="time" size={14} color={st.color} />}
        {st.text}{!outcome ? <> {mm}:{ss}</> : ''}
      </div>
      <div style={{ flex: 1 }} />
      {!outcome ? (
        <button onClick={onHangUp} style={{
          width: 72, height: 72, borderRadius: '50%',
          background: 'var(--md-error)', color: '#fff',
          border: 'none', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          boxShadow: '0 6px 16px rgba(186,26,26,0.4)'
        }}>
          <Icon name="phone-end" size={32} color="#fff" />
        </button>
      ) : (
        <div style={{ fontSize: 14, color: '#b9c5c0' }}>возврат…</div>
      )}
    </div>
  );
}

export function IncomingCallScreen({ incoming, onAccept, onDecline }) {
  if (!incoming) return null;
  const { from } = incoming;
  const name = from.alias || from.name || from.user_id;

  return (
    <div style={{ flex: 1, background: '#0e1614', display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '56px 24px 32px' }}>
      <div style={{ fontSize: 12, letterSpacing: 1.5, color: '#b9c5c0', textTransform: 'uppercase', marginBottom: 32 }}>
        Входящий звонок
      </div>
      <Avatar name={name} size="xl" />
      <div style={{ fontSize: 32, fontWeight: 400, color: '#fff', marginTop: 24 }}>{name}</div>
      <div style={{ fontSize: 14, color: '#b9c5c0', marginTop: 8, display: 'flex', alignItems: 'center', gap: 6 }}>
        <Icon name="lock" size={14} color="var(--md-primary-container)" />
        end-to-end шифрование
      </div>
      <div style={{ flex: 1 }} />
      <div style={{ display: 'flex', justifyContent: 'space-between', width: '100%', alignItems: 'center' }}>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
          <button onClick={onDecline} style={{
            width: 72, height: 72, borderRadius: '50%', background: 'var(--md-error)',
            border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            boxShadow: '0 6px 16px rgba(186,26,26,0.4)'
          }}>
            <Icon name="phone-end" size={28} color="#fff" />
          </button>
          <div style={{ fontSize: 13, color: '#b9c5c0' }}>Отклонить</div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
          <button onClick={onAccept} style={{
            width: 72, height: 72, borderRadius: '50%', background: '#3ddc84',
            border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            boxShadow: '0 6px 16px rgba(61,220,132,0.35)'
          }}>
            <Icon name="phone" size={28} color="#063" />
          </button>
          <div style={{ fontSize: 13, color: '#b9c5c0' }}>Принять</div>
        </div>
      </div>
    </div>
  );
}

export function InCallScreen({ go, contactId, onHangUp }) {
  const [t, setT] = useState(0);
  const [mute, setMute] = useState(false);
  const [spk, setSpk] = useState(true);
  const [callState, setCallState] = useState('active');
  const [outcome, setOutcome] = useState('');
  const nameRef = useRef('');
  const startRef = useRef(Date.now());

  useEffect(() => {
    api.listContacts().then(list => {
      const c = list.find(x => x.user_id === contactId || x.alias === contactId);
      if (c) nameRef.current = c.alias || c.name;
    }).catch(() => {});
  }, [contactId]);

  useEffect(() => {
    const timer = setInterval(() => setT(Math.floor((Date.now() - startRef.current) / 1000)), 200);
    const unsub = on('call-state', (data) => {
      setCallState(data.state);
    });
    const unsub2 = on('call-ended', (data) => {
      clearInterval(timer);
      setOutcome(data.outcome || 'completed');
      setTimeout(() => go('main'), 2500);
    });
    return () => { clearInterval(timer); unsub(); unsub2(); };
  }, []);

  const handleMute = async () => {
    try {
      const res = await api.toggleMute();
      setMute(res.muted !== undefined ? res : !mute);
    } catch (e) {
      console.error('[toggleMute]', e);
      setMute(!mute);
    }
  };

  const handleSpeaker = () => {
    setSpk(!spk);
    api.setOutputVolume(spk ? 0 : 80).catch(() => {});
  };

  const mm = String(Math.floor(t / 60)).padStart(2, '0');
  const ss = String(t % 60).padStart(2, '0');

  if (outcome) {
    const endText = outcome === 'answered' ? 'Завершён' : outcome === 'declined' ? 'Отклонён' : 'Звонок окончен';
    return (
      <div style={{ flex: 1, background: '#0e1614', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: '24px' }}>
        <Avatar name={nameRef.current} size="xl" />
        <div style={{ fontSize: 28, color: '#fff', marginTop: 24 }}>{nameRef.current}</div>
        <div style={{ fontSize: 16, color: '#b9c5c0', marginTop: 8 }}>{endText} · {mm}:{ss}</div>
      </div>
    );
  }

  return (
    <div style={{ flex: 1, background: '#0e1614', display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '24px 24px 24px' }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8,
        background: 'rgba(0,106,96,0.2)', padding: '6px 14px', borderRadius: 999,
        fontSize: 13, color: 'var(--md-primary-container)'
      }}>
        <Icon name="lock" size={14} color="var(--md-primary-container)" />
        e2e · {mm}:{ss}
      </div>
      <div style={{ flex: 1 }} />
      <Avatar name={nameRef.current} size="xl" />
      <div style={{ fontSize: 28, color: '#fff', marginTop: 24 }}>{nameRef.current}</div>
      <div style={{ fontSize: 13, color: '#b9c5c0', marginTop: 4 }}>{callState}</div>

      {/* Audio level bars */}
      <div style={{ display: 'flex', gap: 5, alignItems: 'flex-end', height: 40, marginTop: 32 }}>
        {[0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map(i => (
          <div key={i} className="bar" style={{
            animationDelay: (i * 0.08) + 's',
            animationDuration: (0.7 + (i % 3) * 0.15) + 's',
            opacity: mute ? 0.3 : 1
          }} />
        ))}
      </div>

      <div style={{ flex: 1 }} />

      <div style={{ display: 'flex', justifyContent: 'center', gap: 16 }}>
        <CallBtn icon={mute ? 'mic-off' : 'mic'} active={!mute} onClick={handleMute} label="Mic" />
        <CallBtn icon="speaker" active={spk} onClick={handleSpeaker} label="Speaker" />
        <CallBtn icon="shield" onClick={() => go('safety', { id: contactId })} label="Safety" />
        <CallBtn icon="phone-end" danger onClick={onHangUp} label="End" />
      </div>
    </div>
  );
}

function CallBtn({ icon, active, danger, onClick, label }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
      <button onClick={onClick} style={{
        width: 64, height: 64, borderRadius: '50%',
        background: danger ? 'var(--md-error)' : (active ? '#fff' : 'rgba(255,255,255,0.12)'),
        color: danger ? '#fff' : (active ? '#0e1614' : '#fff'),
        border: 'none', cursor: 'pointer',
        display: 'flex', alignItems: 'center', justifyContent: 'center'
      }}>
        <Icon name={icon} size={26} color="currentColor" />
      </button>
      <div style={{ fontSize: 11, color: '#b9c5c0' }}>{label}</div>
    </div>
  );
}

// ---- Safety Number ----
export function SafetyNumberScreen({ go, contactId }) {
  const [c, setC] = useState(null);
  const [safetyText, setSafetyText] = useState('');

  useEffect(() => {
    api.listContacts().then(list => {
      const found = list.find(x => x.user_id === contactId || x.alias === contactId);
      setC(found || null);
    }).catch(() => {});
    api.safetyNumber(contactId).then(setSafetyText).catch(() => {});
  }, [contactId]);

  if (!c) return <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
    <AppBar title="Safety number" onBack={() => go('contact', { id: contactId })} />
    <div style={{ padding: 24 }}>Загрузка…</div>
  </div>;

  const handleVerify = async (ok) => {
    await api.markVerified(c.user_id, ok);
    go('contact', { id: c.user_id });
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Safety number" onBack={() => go('contact', { id: contactId })} />
      <div style={{ flex: 1, padding: '0 24px 24px', display: 'flex', flexDirection: 'column' }}>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', textAlign: 'center' }}>
          {c.alias || c.name} · вы
        </div>
        <div className="card-outlined" style={{ marginTop: 16, padding: 24, display: 'flex', justifyContent: 'center' }}>
          <div className="mono" style={{
            fontSize: 22, lineHeight: 1.6, color: 'var(--md-on-surface)',
            letterSpacing: 2, textAlign: 'center', whiteSpace: 'pre-line'
          }}>
            {safetyText}
          </div>
        </div>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginTop: 16, lineHeight: 1.5, textAlign: 'center' }}>
          Прочитайте эти буквы по голосу друг другу.<br />
          Если совпадает — контакт подтверждён.
        </div>
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <button className="btn btn-filled btn-block" onClick={() => handleVerify(true)}>
            <Icon name="check" size={18} color="#fff" /> Совпадает
          </button>
          <button className="btn btn-error btn-block" onClick={() => handleVerify(false)}>
            <Icon name="warning" size={18} color="var(--md-on-error-container)" /> Не совпадает
          </button>
        </div>
      </div>
    </div>
  );
}
