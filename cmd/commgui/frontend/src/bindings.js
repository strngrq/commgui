// Wails Go-биндинги — обёртки над window.go.main.App.*

const App = () => window.go.main.App;

export const api = {
  // bootstrap
  bootstrap:      ()              => App().Bootstrap(),
  previewInvite:  (url)           => App().PreviewInvite(url),
  register:       (url, name)     => App().Register(url, name),

  // contacts
  listContacts:   ()              => App().ListContacts(),
  previewContact: (url)           => App().PreviewContact(url),
  addContact:     (url, alias)    => App().AddContact(url, alias),
  renameContact:  (id, alias)     => App().RenameContact(id, alias),
  removeContact:  (id)            => App().RemoveContact(id),
  safetyNumber:   (id)            => App().SafetyNumber(id),
  markVerified:   (id, ok)        => App().MarkVerified(id, ok),

  // invites
  listInvites:    (status)        => App().ListInvites(status || 'active'),
  createInvite:   (opts)          => App().CreateInvite(opts),
  revokeInvite:   (token)         => App().RevokeInvite(token),

  // profile
  profile:        ()              => App().Profile(),
  rename:         (name)          => App().Rename(name),
  myContact:      ()              => App().MyContact(),
  deleteProfile:  ()              => App().DeleteProfile(),

  // server / settings
  serverStatus:   ()              => App().ServerStatus(),
  listAudioDevices: ()            => App().ListAudioDevices(),
  saveAudioPrefs: (p)             => App().SaveAudioPreferences(p),
  getAudioPrefs:  ()              => App().GetAudioPreferences(),
  testMic:        (s)             => App().TestMic(s || 2),
  setRingtone:    (on)            => App().SetRingtone(on),

  // calls
  startCall:      (contactId)     => App().StartCall(contactId),
  acceptCall:     (callId)        => App().AcceptCall(callId),
  declineCall:    (callId)        => App().DeclineCall(callId),
  hangUp:         ()              => App().HangUp(),
  toggleMute:     ()              => App().ToggleMute(),
  setOutputVolume: (v)            => App().SetOutputVolume(v),
  listCalls:      (limit, offset) => App().ListCalls(limit || 50, offset || 0),

  // util
  clipboard:      (text)          => App().Clipboard(text),
};

// Подписка на события из Go.
//
// Wails v2 EventsOff(eventName, ...moreNames) трактует ВСЕ аргументы как
// имена событий и удаляет ВСЕХ подписчиков указанных kinds; передавать туда
// callback бессмысленно и опасно — снесёт чужие подписки на тот же kind.
// Поэтому делаем свой fan-out: один глобальный runtime-listener на kind,
// локальный Set callback'ов; unsub лишь удаляет cb из Set, не трогая runtime.
const subs = new Map(); // kind -> Set<cb>

export const on = (kind, cb) => {
  let set = subs.get(kind);
  if (!set) {
    set = new Set();
    subs.set(kind, set);
    window.runtime.EventsOn(kind, (...args) => {
      const current = subs.get(kind);
      if (!current) return;
      // Копия — чтобы unsub во время dispatch не ломал итерацию.
      for (const fn of Array.from(current)) {
        try { fn(...args); } catch (e) { console.error('[on:' + kind + ']', e); }
      }
    });
  }
  set.add(cb);
  return () => {
    const current = subs.get(kind);
    if (current) current.delete(cb);
  };
};
