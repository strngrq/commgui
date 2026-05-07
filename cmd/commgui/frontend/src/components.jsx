import React from 'react';
import Icon from './icons';

// ==============================================================================
// Shared UI components — AppBar, Avatar, BottomNav, etc.
// ==============================================================================

// Top app bar
export function AppBar({ title, onBack, trailing, dark }) {
  return (
    <div className="app-bar">
      {onBack ? (
        <button className={"icon-btn" + (dark ? " dark" : "")} onClick={onBack}>
          <Icon name="arrow-back" />
        </button>
      ) : <div style={{ width: 16 }} />}
      <span className="title" style={dark ? { color: '#e7eeea' } : undefined}>{title}</span>
      {trailing}
    </div>
  );
}

// Avatar with deterministic color
const palette = ['#006a60', '#7d5260', '#46617a', '#7e5700', '#5b6940', '#7c4f4a', '#5d5c93'];
const colorFor = (s) => palette[(s || '?').charCodeAt(0) % palette.length];

export function Avatar({ name, size = 'md' }) {
  const initial = (name || '?').trim().charAt(0).toUpperCase();
  return (
    <div className={"avatar avatar-" + size} style={{ background: colorFor(name) }}>
      {initial}
    </div>
  );
}

export { colorFor };

// Bottom navigation
export function BottomNav({ active, onChange }) {
  const items = [
    { id: 'contacts', label: 'Контакты', icon: 'people' },
    { id: 'calls',    label: 'Звонки',   icon: 'phone' },
    { id: 'invites',  label: 'Инвайты',  icon: 'ticket' },
    { id: 'profile',  label: 'Профиль',  icon: 'person' },
    { id: 'settings', label: 'Настройки', icon: 'settings' },
  ];
  return (
    <div className="bottom-nav">
      {items.map(it => (
        <button
          key={it.id}
          className={"bottom-nav-item" + (active === it.id ? " active" : "")}
          onClick={() => onChange(it.id)}
        >
          <div className="indicator">
            <Icon name={it.icon} size={22} stroke={active === it.id ? 2.2 : 1.7} />
          </div>
          <div className="label">{it.label}</div>
        </button>
      ))}
    </div>
  );
}

// Connection banner
export function ConnBanner({ status }) {
  if (status === 'connected') return null;
  if (status === 'reconnect') return (
    <div className="conn-banner warn">
      <div style={{ width: 8, height: 8, borderRadius: '50%', background: '#b25f00' }} />
      Восстанавливаем соединение…
    </div>
  );
  return (
    <div className="conn-banner error">
      <div style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--md-error)' }} />
      Нет связи с сервером
    </div>
  );
}

// Section header
export function SectionHeader({ children }) {
  return <div className="section-h">{children}</div>;
}

// Fake QR code (deterministic pattern for mockups)
export function FakeQR({ size = 200, seed = 0 }) {
  const cells = [];
  const rng = (i) => {
    let x = (i * 9301 + 49297 + seed * 1000) % 233280;
    return x / 233280;
  };
  for (let r = 0; r < 25; r++) {
    for (let c = 0; c < 25; c++) {
      const isFinder = (r < 7 && c < 7) || (r < 7 && c > 17) || (r > 17 && c < 7);
      let on = false;
      if (isFinder) {
        const lr = r > 17 ? r - 18 : r;
        const lc = c > 17 ? c - 18 : c;
        on = (lr === 0 || lr === 6 || lc === 0 || lc === 6) || (lr >= 2 && lr <= 4 && lc >= 2 && lc <= 4);
      } else {
        on = rng(r * 25 + c) > 0.55;
      }
      cells.push(<i key={r * 25 + c} style={{ visibility: on ? 'visible' : 'hidden' }} />);
    }
  }
  return <div className="qr-real" style={{ width: size, height: size }}>{cells}</div>;
}

// Switch toggle
export function Toggle({ on, onClick }) {
  return (
    <div
      className={"switch" + (on ? ' on' : '')}
      onClick={(e) => { e.stopPropagation(); onClick(!on); }}
    />
  );
}
