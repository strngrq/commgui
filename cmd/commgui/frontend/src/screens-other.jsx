import React, { useState, useEffect } from 'react';
import Icon from './icons';
import { Avatar, AppBar, Toggle, SectionHeader, ConfirmDialog } from './components';
import { api } from './bindings';

// ==============================================================================
// Other screens: CreateInvite, InviteCreated, MyContact, Settings, ServerInfo
// ==============================================================================

// ---- Create Invite ----
export function CreateInviteScreen({ go }) {
  const [label, setLabel] = useState('');
  const [ttl, setTtl] = useState('24h');
  const [uses, setUses] = useState(1);
  const [creating, setCreating] = useState(false);

  const handleCreate = async () => {
    setCreating(true);
    try {
      const res = await api.createInvite({ label: label.trim() || 'Без метки', ttl, maxUses: uses });
      go('invite-created', { inviteUrl: res.url, inviteToken: res.token });
    } catch (e) {
      alert('Ошибка: ' + (e.message || e));
    } finally {
      setCreating(false);
    }
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Новое приглашение" onBack={() => go('main')} />
      <div style={{ flex: 1, padding: '8px 24px 24px', display: 'flex', flexDirection: 'column' }}>
        <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', marginBottom: 8 }}>
          Метка для вас (на сервер не отправляется)
        </div>
        <div className="field-wrap">
          <input className="field-input" value={label} onChange={e => setLabel(e.target.value)}
            style={{ width: '100%' }} placeholder="Кому?" />
          <span className="field-label">Кому?</span>
        </div>

        <div style={{ marginTop: 24, fontSize: 14, fontWeight: 500, color: 'var(--md-on-surface)' }}>Сколько действует?</div>
        <div className="chips" style={{ marginTop: 8 }}>
          {[['1h', '1 час'], ['24h', '24 часа'], ['7d', '7 дней']].map(([k, v]) => (
            <button key={k} className={"chip" + (ttl === k ? ' selected' : '')} onClick={() => setTtl(k)}>
              {ttl === k && <Icon name="check" size={16} />} {v}
            </button>
          ))}
        </div>

        <div style={{ marginTop: 24, fontSize: 14, fontWeight: 500, color: 'var(--md-on-surface)' }}>Сколько раз можно использовать?</div>
        <div className="chips" style={{ marginTop: 8 }}>
          {[1, 3, 10].map(n => (
            <button key={n} className={"chip" + (uses === n ? ' selected' : '')} onClick={() => setUses(n)}>
              {uses === n && <Icon name="check" size={16} />} {n}
            </button>
          ))}
        </div>

        <div style={{ flex: 1 }} />
        <button className="btn btn-filled btn-block" onClick={handleCreate} disabled={creating}>
          {creating ? 'Создание…' : 'Создать'}
        </button>
      </div>
    </div>
  );
}

// ---- Invite Created ----
export function InviteCreatedScreen({ go, inviteUrl }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await api.clipboard(inviteUrl || '');
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (_) {}
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Готово!" onBack={() => go('main')} />
      <div style={{ flex: 1, padding: '0 24px 24px', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
        <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)', textAlign: 'center', marginBottom: 16 }}>
          Отправьте ссылку приглашённому
        </div>
        <div className="card-outlined mono" style={{
          marginTop: 16, fontSize: 12, width: '100%',
          wordBreak: 'break-all', color: 'var(--md-on-surface)', lineHeight: 1.5, padding: 16
        }}>
          {inviteUrl || ''}
        </div>
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, width: '100%' }}>
          <button className="btn btn-filled btn-block" onClick={handleCopy}>
            <Icon name="copy" size={18} color="#fff" /> {copied ? 'Скопировано!' : 'Скопировать'}
          </button>
          <button className="btn btn-text btn-block" onClick={() => go('main')}>Готово</button>
        </div>
      </div>
    </div>
  );
}

// ---- My Contact ----
export function MyContactScreen({ go }) {
  const [data, setData] = useState(null);

  useEffect(() => {
    api.myContact().then(setData).catch(() => {});
  }, []);

  const handleCopy = async () => {
    if (!data) return;
    try { await api.clipboard(data.contact_url); } catch (_) {}
  };

  if (!data) return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Мой контакт" onBack={() => go('main')} />
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--md-on-surface-variant)' }}>Загрузка…</div>
    </div>
  );

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Мой контакт" onBack={() => go('main')} />
      <div style={{ flex: 1, padding: '0 24px 24px', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
        <div style={{ marginTop: 16, padding: 16, background: '#fff', borderRadius: 24, boxShadow: '0 2px 8px rgba(0,0,0,0.08)' }}>
          <img src={'data:image/png;base64,' + data.qr_png_b64} alt="QR" style={{ width: 220, height: 220 }} />
        </div>
        <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)', marginTop: 16, textAlign: 'center', maxWidth: 280 }}>
          Покажите QR другу — он добавит вас в свои контакты.
        </div>
        <div className="card-outlined mono" style={{
          marginTop: 12, fontSize: 11, width: '100%',
          wordBreak: 'break-all', color: 'var(--md-on-surface)', lineHeight: 1.4, padding: 12
        }}>
          {data.contact_url}
        </div>
        <div style={{ flex: 1 }} />
        <button className="btn btn-tonal btn-block" onClick={handleCopy}>
          <Icon name="copy" size={18} /> Скопировать ссылку
        </button>
      </div>
    </div>
  );
}

// ---- Settings ----
export function SettingsScreen({ go }) {
  const [ringing, setRinging] = useState(true);
  const [audioDevices, setAudioDevices] = useState(null);
  const [inputId, setInputId] = useState('');
  const [outputId, setOutputId] = useState('');
  const [bufferMs, setBufferMs] = useState(640);
  const [prebufMs, setPrebufMs] = useState(200);
  const [debugLog, setDebugLog] = useState(false);
  const [echoCancel, setEchoCancel] = useState(true);
  const [aecTailMs, setAecTailMs] = useState(200);
  const [serverInfo, setServerInfo] = useState(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    api.listAudioDevices().then(setAudioDevices).catch(() => {});
    api.serverStatus().then(setServerInfo).catch(() => {});
    api.getAudioPrefs().then(p => {
      if (p) {
        setInputId(p.inputDeviceId || '');
        setOutputId(p.outputDeviceId || '');
        setBufferMs(p.playbackBufferMs || 640);
        setPrebufMs(p.playbackPrebufMs || 200);
        setDebugLog(!!p.debugLogEnabled);
        setEchoCancel(p.echoCancellation === undefined ? true : !!p.echoCancellation);
        setAecTailMs(p.aecTailMs || 200);
      }
    }).catch(() => {});
  }, []);

  const saveAll = (overrides = {}) => {
    const payload = {
      inputDeviceId: inputId,
      outputDeviceId: outputId,
      playbackBufferMs: bufferMs,
      playbackPrebufMs: prebufMs,
      echoCancellation: echoCancel,
      aecTailMs: aecTailMs,
      debugLogEnabled: debugLog,
      ...overrides,
    };
    api.saveAudioPrefs(payload).catch(() => {});
  };
  const handleSaveAudio = (inp, out) => saveAll({ inputDeviceId: inp, outputDeviceId: out });

  const serverName = serverInfo?.server_url || '—';
  const serverFP = serverInfo?.server_fp || '';

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Настройки" onBack={() => go('main')} />
      <div style={{ flex: 1, overflow: 'auto' }}>
        {audioDevices && (
          <>
            <div style={{ padding: '8px 16px' }}>
              <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>Микрофон</div>
              <select value={inputId} onChange={e => { const v = e.target.value; setInputId(v); handleSaveAudio(v, outputId); }}
                style={{ width: '100%', border: '1px solid var(--md-outline)', borderRadius: 8, padding: '8px',
                  background: 'transparent', fontFamily: 'inherit', fontSize: 14 }}>
                <option value="">Default</option>
                {(audioDevices.input || []).map(d => (
                  <option key={d.id} value={d.id}>{d.name}{d.isDefault ? ' (default)' : ''}</option>
                ))}
              </select>
            </div>
            <div style={{ padding: '8px 16px' }}>
              <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>Воспроизведение</div>
              <select value={outputId} onChange={e => { const v = e.target.value; setOutputId(v); handleSaveAudio(inputId, v); }}
                style={{ width: '100%', border: '1px solid var(--md-outline)', borderRadius: 8, padding: '8px',
                  background: 'transparent', fontFamily: 'inherit', fontSize: 14 }}>
                <option value="">Default</option>
                {(audioDevices.output || []).map(d => (
                  <option key={d.id} value={d.id}>{d.name}{d.isDefault ? ' (default)' : ''}</option>
                ))}
              </select>
            </div>
          </>
        )}
        {!audioDevices && <div style={{ padding: 16, fontSize: 13, color: 'var(--md-on-surface-variant)' }}>Загрузка устройств…</div>}

        <div style={{ padding: '8px 16px' }}>
          <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>
            🧊Размер playback-буфера, мс <span style={{ opacity: 0.6 }}>(80–4000, рек. 640)</span>
          </div>
          <input type="number" min={80} max={4000} step={20} value={bufferMs}
            onChange={e => setBufferMs(parseInt(e.target.value, 10) || 0)}
            onBlur={() => saveAll()}
            style={{ width: '100%', border: '1px solid var(--md-outline)', borderRadius: 8, padding: '8px',
              background: 'transparent', fontFamily: 'inherit', fontSize: 14 }} />
        </div>
        <div style={{ padding: '8px 16px' }}>
          <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>
            Задержка (pre-buffer), мс <span style={{ opacity: 0.6 }}>(20–{Math.floor(bufferMs/2)}, рек. 200)</span>
          </div>
          <input type="number" min={20} max={Math.floor(bufferMs/2)} step={20} value={prebufMs}
            onChange={e => setPrebufMs(parseInt(e.target.value, 10) || 0)}
            onBlur={() => saveAll()}
            style={{ width: '100%', border: '1px solid var(--md-outline)', borderRadius: 8, padding: '8px',
              background: 'transparent', fontFamily: 'inherit', fontSize: 14 }} />
        </div>

        <SettingsRow icon="bell" title="Звук звонка" desc="Входящий вызов" value={
          <Toggle on={ringing} onClick={v => { setRinging(v); api.setRingtone(v).catch(() => {}); }} />
        } />

        <SettingsRow icon="mic-off" title="Подавление эха" desc="SpeexDSP AEC (акустическое эхоподавление)" value={
          <Toggle on={echoCancel} onClick={v => { setEchoCancel(v); saveAll({ echoCancellation: v }); }} />
        } />

        {echoCancel && (
          <div style={{ padding: '8px 16px' }}>
            <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginBottom: 4 }}>
              Длина AEC-фильтра, мс <span style={{ opacity: 0.6 }}>(50–500, рек. 200)</span>
            </div>
            <input type="number" min={50} max={500} step={10} value={aecTailMs}
              onChange={e => setAecTailMs(parseInt(e.target.value, 10) || 0)}
              onBlur={() => saveAll()}
              style={{ width: '100%', border: '1px solid var(--md-outline)', borderRadius: 8, padding: '8px',
                background: 'transparent', fontFamily: 'inherit', fontSize: 14 }} />
          </div>
        )}

        <SettingsRow icon="warning" title="Debug-лог" desc="Запись событий в debug.log" value={
          <Toggle on={debugLog} onClick={v => { setDebugLog(v); saveAll({ debugLogEnabled: v }); }} />
        } />

        <SettingsRow icon="globe" title={serverName} desc={serverFP ? 'FP: ' + serverFP.substring(0, 20) + '…' : ''} value={
          <Icon name="chevron-right" size={20} color="var(--md-on-surface-variant)" />
        } onClick={() => go('server-info')} />

        <SettingsRow icon="logout" title="Удалить профиль" danger onClick={() => setConfirmDelete(true)} />

        <div style={{ height: 24 }} />
      </div>
      <ConfirmDialog
        open={confirmDelete}
        title="Удалить профиль?"
        message="Все локальные данные будут удалены."
        confirmText="Удалить"
        danger
        onConfirm={() => {
          setConfirmDelete(false);
          api.deleteProfile().then(() => go('welcome')).catch(e => {
            alert('Ошибка удаления профиля: ' + (e?.message || e || ''));
          });
        }}
        onCancel={() => setConfirmDelete(false)}
      />
    </div>
  );
}

function SettingsRow({ icon, title, desc, value, onClick, danger }) {
  return (
    <div className="list-item" onClick={onClick} style={{ minHeight: 64, cursor: onClick ? 'pointer' : 'default' }}>
      {icon && (
        <div style={{ width: 24, display: 'flex', justifyContent: 'center' }}>
          <Icon name={icon} size={22} color={danger ? 'var(--md-error)' : 'var(--md-on-surface-variant)'} />
        </div>
      )}
      <div className="lines">
        <div className="headline" style={{ color: danger ? 'var(--md-error)' : undefined }}>{title}</div>
        {desc && <div className="supporting">{desc}</div>}
      </div>
      {value !== undefined && (
        typeof value === 'string'
          ? <div style={{ fontSize: 14, color: 'var(--md-on-surface-variant)' }}>{value}</div>
          : value
      )}
    </div>
  );
}

// ---- Server Info ----
export function ServerInfoScreen({ go }) {
  const [info, setInfo] = useState(null);

  useEffect(() => {
    api.serverStatus().then(setInfo).catch(() => {});
  }, []);

  if (!info) return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Сервер" onBack={() => go('settings')} />
      <div style={{ padding: 24, color: 'var(--md-on-surface-variant)' }}>Загрузка…</div>
    </div>
  );

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      <AppBar title="Сервер" onBack={() => go('settings')} />
      <div style={{ flex: 1, padding: '8px 24px 24px', overflow: 'auto' }}>
        <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)' }}>Адрес</div>
        <div style={{ fontSize: 18, color: 'var(--md-on-surface)', marginTop: 4, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Icon name="globe" size={18} color="var(--md-primary)" /> {info.server_url}
        </div>

        <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginTop: 24 }}>Серверный fingerprint</div>
        <div className="card-outlined mono" style={{
          marginTop: 8, fontSize: 16, color: 'var(--md-on-surface)', letterSpacing: 1.5, lineHeight: 1.6
        }}>
          {info.server_fp}
        </div>

        <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginTop: 24 }}>Статус</div>
        <div className="card" style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <StatusLine label="Push (WS)" online={info.push_ws} text={info.push_ws ? 'подключён' : 'недоступен'} />
        </div>

        {info.version && (
          <>
            <div style={{ fontSize: 12, color: 'var(--md-on-surface-variant)', marginTop: 24 }}>Версия</div>
            <div className="card-outlined" style={{ marginTop: 8, fontSize: 14 }}>
              commsrv {info.version}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function StatusLine({ label, online, text }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      <div style={{
        width: 10, height: 10, borderRadius: '50%',
        background: online ? '#3ddc84' : 'var(--md-error)',
        boxShadow: online ? '0 0 8px #3ddc84' : 'none'
      }} />
      <div style={{ fontSize: 14, color: 'var(--md-on-surface)', minWidth: 80 }}>{label}</div>
      <div style={{ fontSize: 13, color: 'var(--md-on-surface-variant)' }}>{text}</div>
    </div>
  );
}
