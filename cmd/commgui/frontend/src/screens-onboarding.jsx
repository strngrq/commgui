import React, { useState, useEffect } from 'react';
import Icon from './icons';
import { AppBar, Avatar } from './components';

// ==============================================================================
// Onboarding screens: Splash, Welcome, PasteInvite, InvitePreview, Registration
// ==============================================================================

export function SplashScreen() {
  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 24 }}>
      <div style={{
        width: 120, height: 120, borderRadius: 32,
        background: 'var(--md-primary)', color: '#fff',
        display: 'flex', alignItems: 'center', justifyContent: 'center'
      }}>
        <Icon name="phone" size={56} color="#fff" />
      </div>
      <div style={{ textAlign: 'center' }}>
        <div style={{ fontSize: 28, fontWeight: 500, color: 'var(--md-on-surface)' }}>privcall</div>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginTop: 4 }}>Приватные голосовые звонки</div>
      </div>
      <div style={{ marginTop: 32, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 16 }}>
        <div style={{ display: 'flex', gap: 6, alignItems: 'flex-end', height: 40 }}>
          {[0, 1, 2, 3].map(i => (
            <div key={i} className="bar" style={{ animationDelay: i * 0.15 + 's', background: 'var(--md-primary)' }} />
          ))}
        </div>
        <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)' }}>загрузка…</div>
      </div>
    </div>
  );
}

export function WelcomeScreen({ onPaste }) {
  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: '32px 24px', justifyContent: 'space-between' }}>
      <div>
        <div style={{ fontSize: 16, color: 'var(--md-on-surface-variant)', marginTop: 16, lineHeight: 1.5 }}>
          Чтобы войти, нужен invite от того, кто уже на сервере.
        </div>
        <div className="card-outlined" style={{ marginTop: 24, display: 'flex', gap: 12, alignItems: 'flex-start' }}>
          <Icon name="lock" size={20} color="var(--md-primary)" />
          <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', lineHeight: 1.5 }}>
            Приложение для защищенных голосовых звонков. Регистрация без e-mail и телефона. Звонки шифруются на ваших устройствах.
          </div>
        </div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <button className="btn btn-filled btn-block" onClick={onPaste}>
          <Icon name="paste" size={18} color="#fff" /> Вставить invite-URL
        </button>
      </div>
    </div>
  );
}

export function PasteUrlScreen({ onContinue, onBack }) {
  const [url, setUrl] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    navigator.clipboard?.readText().then(text => {
      if (text && text.startsWith('privcall://')) setUrl(text);
    }).catch(() => {});
  }, []);

  const handleContinue = () => {
    if (!url.trim()) { setError('Вставьте invite-URL'); return; }
    if (!url.startsWith('privcall://invite')) {
      setError('Должен начинаться с privcall://invite'); return;
    }
    onContinue(url.trim());
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Вставить invite-URL" onBack={onBack} />
      <div style={{ flex: 1, padding: '8px 24px 24px', display: 'flex', flexDirection: 'column' }}>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginBottom: 16, lineHeight: 1.5 }}>
          Вставьте invite-URL, который вам прислали в любом мессенджере.
        </div>
        <div className="field-wrap">
          <textarea
            value={url}
            onChange={e => { setUrl(e.target.value); setError(''); }}
            className="field-input mono"
            style={{ width: '100%', height: 120, padding: 16, fontSize: 13, resize: 'none', lineHeight: 1.5 }}
            placeholder="privcall://invite#..."
          />
          <span className="field-label">privcall://...</span>
        </div>
        {error && <div style={{ color: 'var(--md-error)', fontSize: 13, marginTop: 8 }}>{error}</div>}
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <button className="btn btn-filled btn-block" onClick={handleContinue}>Продолжить</button>
          <button className="btn btn-text btn-block" onClick={onBack}>Отмена</button>
        </div>
      </div>
    </div>
  );
}

export function InvitePreviewScreen({ preview, onAccept, onBack }) {
  if (!preview) {
    return (
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        <AppBar onBack={onBack} />
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--md-on-surface-variant)' }}>Загрузка…</div>
      </div>
    );
  }

  if (!preview.valid) {
    return (
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        <AppBar onBack={onBack} />
        <div style={{ flex: 1, padding: 24, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', textAlign: 'center' }}>
          <div style={{ fontSize: 20, color: 'var(--md-error)' }}>Приглашение недействительно</div>
          <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginTop: 8 }}>Просрочено, отозвано или уже использовано.</div>
          <button className="btn btn-tonal" style={{ marginTop: 24 }} onClick={onBack}>Назад</button>
        </div>
      </div>
    );
  }

  const expiresText = preview.expires_at
    ? new Date(preview.expires_at * 1000).toLocaleString('ru')
    : 'неизвестно';

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar onBack={onBack} />
      <div style={{ flex: 1, padding: '0 24px', display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center' }}>
        <Avatar name={preview.issued_by_name || '?'} size="xl" />
        <div style={{ fontSize: 28, fontWeight: 400, color: 'var(--md-on-surface)', marginTop: 24 }}>
          {preview.issued_by_name || 'Кто-то'} приглашает вас
        </div>
        <div style={{ fontSize: 16, color: 'var(--md-on-surface-variant)', marginTop: 8 }}>
          на сервер <span style={{ color: 'var(--md-on-surface)' }}>{preview.server_url}</span>
        </div>

        <div className="card-outlined" style={{ width: '100%', marginTop: 24, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, fontSize: 14, color: 'var(--md-on-surface-variant)' }}>
            <Icon name="time" size={18} />
            <div style={{ flex: 1, textAlign: 'left' }}>Действует до {expiresText}</div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, fontSize: 14, color: 'var(--md-on-surface-variant)' }}>
            <Icon name="ticket" size={18} />
            <div style={{ flex: 1, textAlign: 'left' }}>{preview.remaining_uses} использований</div>
          </div>
        </div>

        <div className="card-outlined" style={{ width: '100%', marginTop: 16, textAlign: 'left' }}>
          <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', display: 'flex', alignItems: 'center', gap: 6 }}>
            <Icon name="fingerprint" size={14} /> Серверный fingerprint
          </div>
          <div className="mono" style={{ fontSize: 16, marginTop: 4, color: 'var(--md-on-surface)', letterSpacing: 1 }}>
            {preview.server_fp}
          </div>
        </div>

        <div style={{ flex: 1 }} />
        <div style={{ width: '100%', display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 24 }}>
          <button className="btn btn-filled btn-block" onClick={onAccept}>Принять и продолжить</button>
          <button className="btn btn-text btn-block" onClick={onBack}>Отмена</button>
        </div>
      </div>
    </div>
  );
}

export function RegistrationScreen({ inviteUrl, onRegister, onBack }) {
  const [name, setName] = useState('');
  const [loading, setLoading] = useState(false);

  const handleRegister = async () => {
    if (!name.trim()) return;
    setLoading(true);
    await onRegister(inviteUrl, name.trim());
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Регистрация" onBack={onBack} />
      <div style={{ flex: 1, padding: '8px 24px 24px', display: 'flex', flexDirection: 'column' }}>
        <div style={{ fontSize: 22, color: 'var(--md-on-surface)', marginBottom: 8 }}>
          Как вас будут видеть ваши контакты?
        </div>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginBottom: 24 }}>
          Имя можно сменить позже.
        </div>

        <div className="field-wrap">
          <input className="field-input" value={name} onChange={e => setName(e.target.value)}
            style={{ width: '100%' }} placeholder="Ваше имя" autoFocus />
          <span className="field-label">Имя</span>
        </div>
        <div className="field-helper" style={{ marginTop: 4, display: 'flex', justifyContent: 'space-between' }}>
          <span /><span>{name.length}/64</span>
        </div>

        <div className="card" style={{ marginTop: 24, display: 'flex', gap: 12, alignItems: 'flex-start' }}>
          <Icon name="lock" size={18} color="var(--md-primary)" />
          <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', lineHeight: 1.5 }}>
            Сейчас сгенерируется ключевая пара. Приватный ключ хранится локально.
          </div>
        </div>

        <div style={{ flex: 1 }} />
        <button className="btn btn-filled btn-block" disabled={!name.trim() || loading}
          style={{ opacity: name.trim() && !loading ? 1 : 0.5 }}
          onClick={handleRegister}>{loading ? 'Регистрация…' : 'Продолжить'}</button>
      </div>
    </div>
  );
}
