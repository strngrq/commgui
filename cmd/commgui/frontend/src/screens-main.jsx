import React, { useState, useEffect } from 'react';
import Icon from './icons';
import { Avatar, AppBar, ConnBanner, SectionHeader } from './components';
import { api, on } from './bindings';

// ==============================================================================
// Main screens: Contacts, Calls, Invites, Profile, ContactDetail, AddContact
// ==============================================================================

// ---- Contacts Tab ----
export function ContactsTab({ go, conn, onCall, activeCallId, style }) {
  const [contacts, setContacts] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.listContacts().then(setContacts).catch(() => {}).finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    const unsub = on('contact-added', () => {
      api.listContacts().then(setContacts).catch(() => {});
    });
    return unsub;
  }, []);

  return (
    <div style={style}>
      <div className="app-bar large">
        <div className="row">
          <div style={{ width: 16 }} />
          <div style={{ flex: 1 }} />
        </div>
        <div className="big-title">Контакты</div>
      </div>
      <ConnBanner status={conn} />
      <div style={{ flex: 1, overflow: 'auto' }}>
        {loading && <div style={{ padding: 24, color: 'var(--md-on-surface-variant)', fontSize: 14 }}>Загрузка…</div>}
        {!loading && contacts.length === 0 && (
          <div style={{ padding: 24, color: 'var(--md-on-surface-variant)', fontSize: 14 }}>
            Нет контактов. Добавьте первый контакт по кнопке +.
          </div>
        )}
        {contacts.map((c, i) => (
          <React.Fragment key={c.user_id}>
            <ContactItem c={c} onClick={() => go('contact', { id: c.user_id })} onCall={() => onCall(c.user_id)} disabled={!!activeCallId} />
            {i < contacts.length - 1 && <div className="list-divider" />}
          </React.Fragment>
        ))}
      </div>
      <button className="fab" onClick={() => go('add-contact')}>
        <Icon name="add" size={24} color="var(--md-on-primary-container)" />
      </button>
    </div>
  );
}

function ContactItem({ c, onClick, onCall, disabled }) {
  return (
    <div className="list-item" onClick={onClick} style={{ cursor: 'pointer' }}>
      <Avatar name={c.alias || c.name} size="sm" />
      <div className="lines">
        <div className="headline">{c.alias || c.name}</div>
        <div className="supporting">
          {c.verified ? (
            <><Icon name="shield-check" size={14} color="var(--md-primary)" /> verified</>
          ) : (
            <><Icon name="warning" size={14} color="var(--md-warning)" /> не подтверждён</>
          )}
        </div>
      </div>
      <button className="icon-btn" disabled={disabled} onClick={e => { e.stopPropagation(); onCall(); }}
        style={{ opacity: disabled ? 0.3 : 1 }}>
        <Icon name="phone" size={20} color={disabled ? 'var(--md-on-surface-variant)' : 'var(--md-primary)'} />
      </button>
    </div>
  );
}

// ---- Calls Tab ----
export function CallsTab({ go, style, onCall, activeCallId }) {
  const [calls, setCalls] = useState([]);

  useEffect(() => {
    api.listCalls(50, 0).then(setCalls).catch(() => {});
  }, []);

  return (
    <div style={style}>
      <div className="app-bar large">
        <div className="row">
          <div style={{ width: 16 }} />
          <div style={{ flex: 1 }} />
        </div>
        <div className="big-title">Звонки</div>
      </div>
      <div style={{ flex: 1, overflow: 'auto' }}>
        {calls.length === 0 && (
          <div style={{ padding: 24, color: 'var(--md-on-surface-variant)', fontSize: 14 }}>
            История звонков пока пуста.
          </div>
        )}
        {calls.map((c, i) => (
          <React.Fragment key={c.call_id || i}>
            <CallRow entry={c} onCall={onCall} disabled={!!activeCallId} />
            {i < calls.length - 1 && <div className="list-divider" />}
          </React.Fragment>
        ))}
      </div>
    </div>
  );
}

function CallRow({ entry, onCall, disabled }) {
  const missed = entry.outcome === 'missed' || entry.outcome === 'declined';
  const ic = missed ? 'phone-missed' : (entry.direction === 'in' ? 'phone-incoming' : 'phone-outgoing');
  const color = missed ? 'var(--md-error)' : 'var(--md-on-surface-variant)';
  const dirLabel = entry.direction === 'in' ? 'Входящий' : 'Исходящий';
  const dur = entry.duration_ms ? formatDuration(entry.duration_ms) : '0:00';
  const name = entry.peer_name || entry.peer_user_id || '?';
  const dateStr = entry.started_at
    ? new Date(entry.started_at).toLocaleString('ru', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })
    : '';
  const canCall = !!(onCall && entry.peer_user_id);
  return (
    <div className="list-item">
      <Avatar name={name} size="sm" />
      <div className="lines">
        <div className="headline" style={{ color: missed ? 'var(--md-error)' : undefined }}>{name}</div>
        <div className="supporting">
          <Icon name={ic} size={14} color={color} /> {dirLabel} · {dur}{dateStr ? ' · ' + dateStr : ''}
        </div>
      </div>
      {canCall && (
        <button className="icon-btn" disabled={disabled}
          onClick={e => { e.stopPropagation(); onCall(entry.peer_user_id); }}
          style={{ opacity: disabled ? 0.3 : 1 }}>
          <Icon name="phone" size={20} color={disabled ? 'var(--md-on-surface-variant)' : 'var(--md-primary)'} />
        </button>
      )}
    </div>
  );
}

function formatDuration(ms) {
  const s = Math.floor(ms / 1000);
  const m = Math.floor(s / 60);
  return m + ':' + String(s % 60).padStart(2, '0');
}

// ---- Invites Tab ----
export function InvitesTab({ go, style }) {
  const [invites, setInvites] = useState([]);
  const [loading, setLoading] = useState(true);

  const load = () => {
    api.listInvites('all').then(setInvites).catch(() => {}).finally(() => setLoading(false));
  };
  useEffect(load, []);

  const active = invites.filter(i => !i.revoked_at && i.expires_at * 1000 > Date.now());
  const expired = invites.filter(i => !active.includes(i));

  return (
    <div style={style}>
      <div className="app-bar large">
        <div className="row">
          <div style={{ width: 16 }} />
          <div style={{ flex: 1 }} />
        </div>
        <div className="big-title">Мои приглашения</div>
      </div>
      <div style={{ flex: 1, overflow: 'auto' }}>
        {loading && <div style={{ padding: 24, color: 'var(--md-on-surface-variant)', fontSize: 14 }}>Загрузка…</div>}
        {!loading && invites.length === 0 && (
          <div style={{ padding: 24, color: 'var(--md-on-surface-variant)', fontSize: 14 }}>
            Нет приглашений. Создайте новое по кнопке +.
          </div>
        )}
        {active.map((i, idx) => (
          <React.Fragment key={i.token}>
            <InviteRow inv={i} />
            {idx < active.length - 1 && <div className="list-divider" />}
          </React.Fragment>
        ))}
        {expired.length > 0 && <SectionHeader>Истёкшие</SectionHeader>}
        {expired.map((i, idx) => (
          <React.Fragment key={i.token}>
            <InviteRow inv={i} expired />
            {idx < expired.length - 1 && <div className="list-divider" />}
          </React.Fragment>
        ))}
      </div>
      <button className="fab" onClick={() => go('create-invite')}>
        <Icon name="add" size={24} color="var(--md-on-primary-container)" />
      </button>
    </div>
  );
}

function InviteRow({ inv, expired }) {
  const expText = expired ? 'истёк' : ('до ' + new Date(inv.expires_at).toLocaleDateString('ru'));
  return (
    <div className="list-item">
      <div style={{
        width: 40, height: 40, borderRadius: 12,
        background: expired ? 'var(--md-surface-container)' : 'var(--md-secondary-container)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        opacity: expired ? 0.6 : 1
      }}>
        <Icon name="ticket" size={20} color={expired ? 'var(--md-on-surface-variant)' : 'var(--md-on-secondary-container)'} />
      </div>
      <div className="lines">
        <div className="headline">«{inv.label || 'Без метки'}»</div>
        <div className="supporting">{expText} · {inv.used_count}/{inv.max_uses} исп.</div>
      </div>
    </div>
  );
}

// ---- Profile Tab ----
export function ProfileTab({ go, profile, style }) {
  const [info, setInfo] = useState(null);
  const [editing, setEditing] = useState(false);
  const [newName, setNewName] = useState('');

  useEffect(() => {
    api.profile().then(setInfo).catch(() => {});
  }, []);

  const handleRename = async () => {
    if (!newName.trim()) return;
    await api.rename(newName.trim());
    setInfo(prev => ({ ...prev, name: newName.trim() }));
    setEditing(false);
  };

  const handleDeleteProfile = async () => {
    if (!confirm('Удалить профиль? Все локальные данные будут удалены.')) return;
    api.deleteProfile().then(() => go('welcome')).catch(e => {
      alert('Ошибка удаления профиля: ' + (e?.message || e || ''));
    });
  };

  const name = info?.name || profile?.name || '—';
  const server = info?.server || profile?.server || '—';
  const serverFP = profile?.server_fp || '';

  return (
    <div style={style}>
      <div className="app-bar large">
        <div className="row">
          <div style={{ width: 16 }} />
          <div style={{ flex: 1 }} />
        </div>
        <div className="big-title">Профиль</div>
      </div>
      <div style={{ flex: 1, overflow: 'auto', padding: '8px 24px 24px' }}>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
          <Avatar name={name} size="xl" />
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {editing ? (
              <>
                <input className="field-input" value={newName} onChange={e => setNewName(e.target.value)}
                  style={{ width: 160, height: 40, fontSize: 16 }} autoFocus />
                <button className="btn btn-filled" style={{ height: 36, padding: '0 12px', fontSize: 13 }} onClick={handleRename}>OK</button>
                <button className="btn btn-text" style={{ height: 36, fontSize: 13 }} onClick={() => setEditing(false)}>Отмена</button>
              </>
            ) : (
              <>
                <span style={{ fontSize: 24, color: 'var(--md-on-surface)' }}>{name}</span>
                <button className="icon-btn" style={{ width: 32, height: 32 }} onClick={() => { setNewName(name); setEditing(true); }}>
                  <Icon name="edit" size={16} />
                </button>
              </>
            )}
          </div>
          <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', display: 'flex', alignItems: 'center', gap: 6 }}>
            <Icon name="globe" size={14} /> {server}
          </div>
        </div>

        <button className="card" style={{
          marginTop: 24, width: '100%', display: 'flex', alignItems: 'center', gap: 16,
          border: 'none', cursor: 'pointer', fontFamily: 'inherit', textAlign: 'left'
        }} onClick={() => go('my-contact')}>
          <div style={{ width: 48, height: 48, borderRadius: 14, background: 'var(--md-primary-container)',
            display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Icon name="qr" size={24} color="var(--md-on-primary-container)" />
          </div>
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 16, color: 'var(--md-on-surface)' }}>Мой контакт</div>
            <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)' }}>Ссылка и QR код</div>
          </div>
          <Icon name="chevron-right" size={20} color="var(--md-on-surface-variant)" />
        </button>

        {serverFP && (
          <div className="card-outlined" style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)' }}>Серверный fingerprint</div>
            <div className="mono" style={{ fontSize: 16, marginTop: 4, color: 'var(--md-on-surface)' }}>{serverFP}</div>
          </div>
        )}

        <div style={{ marginTop: 24, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <button className="btn btn-text btn-block" style={{ color: 'var(--md-error)' }} onClick={handleDeleteProfile}>
            <Icon name="logout" size={18} color="var(--md-error)" /> Удалить профиль
          </button>
        </div>
      </div>
    </div>
  );
}

// ---- Contact Detail Screen ----
export function ContactDetailScreen({ go, contactId, onCall, activeCallId }) {
  const [c, setC] = useState(null);
  const [editing, setEditing] = useState(false);
  const [alias, setAlias] = useState('');

  useEffect(() => {
    api.listContacts().then(list => {
      const found = list.find(x => x.user_id === contactId || x.alias === contactId);
      setC(found || null);
    }).catch(() => {});
  }, [contactId]);

  if (!c) return <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
    <AppBar onBack={() => go('main')} />
    <div style={{ padding: 24, color: 'var(--md-on-surface-variant)' }}>Контакт не найден.</div>
  </div>;

  const handleRename = async () => {
    await api.renameContact(c.user_id, alias.trim());
    setC(prev => ({ ...prev, alias: alias.trim() }));
    setEditing(false);
  };

  const handleRemove = async () => {
    if (!confirm('Удалить контакт ' + (c.alias || c.name) + '?')) return;
    await api.removeContact(c.user_id);
    go('main');
  };

  const handleVerify = async (ok) => {
    await api.markVerified(c.user_id, ok);
    setC(prev => ({ ...prev, verified: ok }));
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar onBack={() => go('main')} />
      <div style={{ flex: 1, padding: '0 24px 24px', display: 'flex', flexDirection: 'column', alignItems: 'center', maxWidth: 480, margin: '0 auto' }}>
        <Avatar name={c.alias || c.name} size="xl" />
        <div style={{ fontSize: 28, color: 'var(--md-on-surface)', marginTop: 16 }}>{c.alias || c.name}</div>
        <div style={{ fontSize: 14, marginTop: 4, color: c.verified ? 'var(--md-primary)' : 'var(--md-warning)', display: 'flex', alignItems: 'center', gap: 6 }}>
          <Icon name={c.verified ? 'shield-check' : 'warning'} size={16} />
          {c.verified ? 'verified' : 'не подтверждён'}
        </div>

        <div style={{ width: '100%', marginTop: 32, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <button className="btn btn-filled btn-block" disabled={!!activeCallId}
            style={{ opacity: activeCallId ? 0.4 : 1 }}
            onClick={() => onCall(c.user_id)}>
            <Icon name="phone" size={18} color="#fff" /> {activeCallId === 'pending' ? 'Звоним…' : 'Позвонить'}
          </button>
          <button className="btn btn-tonal btn-block" onClick={() => go('safety', { id: c.user_id })}>
            <Icon name="shield" size={18} /> Safety number
          </button>

          {editing ? (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <input className="field-input" value={alias} onChange={e => setAlias(e.target.value)}
                style={{ flex: 1, height: 40, fontSize: 14 }} placeholder="Новое имя" autoFocus />
              <button className="btn btn-filled" style={{ height: 36, padding: '0 12px', fontSize: 13 }} onClick={handleRename}>OK</button>
              <button className="btn btn-text" style={{ height: 36, fontSize: 13 }} onClick={() => setEditing(false)}>Отмена</button>
            </div>
          ) : (
            <button className="btn btn-text btn-block" onClick={() => { setAlias(c.alias || c.name); setEditing(true); }}>
              <Icon name="edit" size={18} color="var(--md-primary)" /> Переименовать локально
            </button>
          )}

          {!c.verified ? (
            <button className="btn btn-text btn-block" onClick={() => handleVerify(true)}>
              <Icon name="shield-check" size={18} color="var(--md-success)" /> Подтвердить контакт
            </button>
          ) : (
            <button className="btn btn-text btn-block" onClick={() => handleVerify(false)}>
              <Icon name="warning" size={18} color="var(--md-warning)" /> Сбросить подтверждение
            </button>
          )}

          <button className="btn btn-text btn-block" style={{ color: 'var(--md-error)' }} onClick={handleRemove}>
            <Icon name="trash" size={18} color="var(--md-error)" /> Удалить контакт
          </button>
        </div>
      </div>
    </div>
  );
}

// ---- Add Contact Screen ----
export function AddContactScreen({ go, onBack }) {
  const [url, setUrl] = useState('');
  const [preview, setPreview] = useState(null);
  const [error, setError] = useState('');
  const [alias, setAlias] = useState('');

  const handleParse = async () => {
    setError('');
    setPreview(null);
    if (!url.trim()) { setError('Вставьте contact-URL'); return; }
    try {
      const p = await api.previewContact(url.trim());
      setPreview(p);
      setAlias(p.name || '');
    } catch (e) {
      setError(e.message || 'Не удалось распознать URL');
    }
  };

  const handleAdd = async () => {
    if (!preview) return;
    try {
      await api.addContact(url.trim(), alias.trim());
      go('main');
    } catch (e) {
      setError(e.message || 'Не удалось добавить контакт');
    }
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Добавить контакт" onBack={onBack} />
      <div style={{ flex: 1, padding: '0 24px 24px', display: 'flex', flexDirection: 'column' }}>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', marginBottom: 12, marginTop: 8, lineHeight: 1.5 }}>
          Вставьте contact-URL:
        </div>
        <div className="field-wrap">
          <textarea value={url} onChange={e => setUrl(e.target.value)}
            className="field-input mono"
            style={{ width: '100%', height: 80, padding: 12, fontSize: 13, resize: 'none' }}
            placeholder="privcall://add#..." />
        </div>
        <button className="btn btn-tonal" style={{ marginTop: 8, alignSelf: 'flex-start' }} onClick={handleParse}>
          Распознать URL
        </button>
        {error && <div style={{ color: 'var(--md-error)', fontSize: 13, marginTop: 8 }}>{error}</div>}

        {preview && (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center', marginTop: 16 }}>
            <Avatar name={preview.name || '?'} size="lg" />
            <div style={{ fontSize: 20, color: 'var(--md-on-surface)', marginTop: 12 }}>{preview.name}</div>
            <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', marginTop: 4, display: 'flex', alignItems: 'center', gap: 6 }}>
              <Icon name="globe" size={14} /> {preview.server}
            </div>

            <div className="card-outlined" style={{ width: '100%', marginTop: 16, textAlign: 'left' }}>
              <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)' }}>Fingerprint</div>
              <div className="mono" style={{ fontSize: 16, marginTop: 4, color: 'var(--md-on-surface)' }}>{preview.fingerprint}</div>
            </div>

            <div className="card" style={{ width: '100%', marginTop: 12, background: '#fff8e1', display: 'flex', gap: 12, alignItems: 'flex-start', textAlign: 'left' }}>
              <Icon name="warning" size={18} color="var(--md-warning)" />
              <div style={{ fontSize: 13, color: 'var(--md-on-surface)', lineHeight: 1.5 }}>
                Контакт станет verified только после сверки safety number в звонке.
              </div>
            </div>

            <div style={{ width: '100%', marginTop: 12, textAlign: 'left' }}>
              <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>Метка (локально)</div>
              <input className="field-input" value={alias} onChange={e => setAlias(e.target.value)}
                style={{ width: '100%', height: 40, fontSize: 14 }} />
            </div>

            <div style={{ width: '100%', display: 'flex', flexDirection: 'column', gap: 8, marginTop: 24 }}>
              <button className="btn btn-filled btn-block" onClick={handleAdd}>Добавить</button>
              <button className="btn btn-text btn-block" onClick={onBack}>Отмена</button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
