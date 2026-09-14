import type { Member } from '../types';
import type { Translator } from '../i18n';
import './MemberList.css';

interface MemberListProps {
  selfMember: Member;
  otherMembers: Member[];
  t: Translator;
}

function MemberRow({ member, t, isSelf = false }: { member: Member; t: Translator; isSelf?: boolean }) {
  const statusLabel = member.statusLabel || (member.sharing ? t('members.sharing') : member.muted ? t('members.muted') : t('members.connected'));
  const clientLabel = member.clientType ? t(member.clientType === 'client' ? 'members.clientTypeClient' : 'members.clientTypeWeb') : '';
  return (
    <li className={`member-item ${isSelf ? 'is-self' : ''}`}>
      <span className={`member-status ${member.online ? 'online' : 'offline'}`}>
        {member.online ? '●' : '○'}
      </span>
      <div className="member-body">
        <span className="member-name-row">
          <span className="member-name">{member.name}</span>
          {isSelf && <span className="member-self-pill">{t('members.self')}</span>}
        </span>
        <span className="member-icons">
          {clientLabel ? `${clientLabel} · ${statusLabel}` : statusLabel}
        </span>
      </div>
    </li>
  );
}

export default function MemberList({
  selfMember,
  otherMembers,
  t,
}: MemberListProps) {
  return (
    <div className="member-list">
      <div className="member-list-header">{t('members.header')}</div>
      <div className="member-group">
        <div className="member-group-header">{t('members.me')}</div>
        <ul className="member-list-items">
          <MemberRow member={selfMember} t={t} isSelf />
        </ul>
      </div>
      <div className="member-group">
        <div className="member-group-header">{t('members.others')}</div>
        {otherMembers.length === 0 ? (
          <div className="member-list-empty">{t('members.empty')}</div>
        ) : (
          <ul className="member-list-items">
            {otherMembers.map((member) => (
              <MemberRow key={member.id} member={member} t={t} />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
