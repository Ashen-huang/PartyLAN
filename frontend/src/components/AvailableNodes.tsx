import type { AvailableNode } from '../types';
import type { Translator } from '../i18n';
import './AvailableNodes.css';

interface AvailableNodesProps {
  nodes: AvailableNode[];
  busy: boolean;
  t: Translator;
  onJoinNode: (targetNodeId: string) => void | Promise<void>;
}

export default function AvailableNodes({ nodes, busy, t, onJoinNode }: AvailableNodesProps) {
  return (
    <div className="available-nodes">
      <div className="available-nodes-header">{t('nodes.header')}</div>
      {nodes.length === 0 ? (
        <div className="available-nodes-empty">{t('nodes.empty')}</div>
      ) : (
        <ul className="available-nodes-list">
          {nodes.map((node) => (
            <li key={node.id} className="available-node-item">
              <div className="available-node-meta">
                <span className="available-node-name">{node.hostname}</span>
                <span className="available-node-ip">{node.ip}</span>
                <span className="available-node-state">
                  {node.joinable
                    ? `${t('nodes.joinable')}${node.roomId ? ` · ${t('nodes.host', { roomId: node.roomId })}` : ''}`
                    : t('nodes.notJoinable')}
                </span>
              </div>
              <button
                className="available-node-btn"
                disabled={busy || !node.online}
                onClick={() => {
                  void onJoinNode(node.id);
                }}
              >
                {node.joinable ? t('nodes.join') : t('nodes.tryJoin')}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
