import React, { useEffect, useMemo, useState } from 'react';
import AdminLayout from '../../components/AdminLayout';
import { useI18n } from '../../contexts/I18nContext';
import PasswordSettingsSection from '../../components/PasswordSettingsSection';
import {
  systemSettingsService,
  type SystemImageSetting,
} from '../../services/systemSettingsService';

const IMAGE_TYPE_OPTIONS = [
  { value: 'openclaw', label: 'OpenClaw' },
  { value: 'ubuntu', label: 'Ubuntu' },
  { value: 'webtop', label: 'Webtop' },
  { value: 'hermes', label: 'Hermes' },
  { value: 'debian', label: 'Debian' },
  { value: 'centos', label: 'CentOS' },
  { value: 'custom', label: 'Custom' },
];

const RUNTIME_TYPE_OPTIONS: Array<{ value: 'desktop' | 'shell'; labelKey: string }> = [
  { value: 'desktop', labelKey: 'instances.runtimeTypeDesktop' },
  { value: 'shell', labelKey: 'instances.runtimeTypeShell' },
];

const DEFAULT_IMAGES: Record<'desktop' | 'shell', Record<string, string>> = {
  desktop: {
    openclaw: 'ghcr.io/yuan-lab-llm/agentsruntime/openclaw:latest',
    ubuntu: 'lscr.io/linuxserver/webtop:ubuntu-xfce',
    webtop: 'lscr.io/linuxserver/webtop:ubuntu-xfce',
    hermes: 'ghcr.io/yuan-lab-llm/agentsruntime/hermes:latest',
    debian: 'docker.io/clawreef/debian-desktop:12',
    centos: 'docker.io/clawreef/centos-desktop:9',
    custom: 'registry.example.com/your-custom-image:latest',
  },
  shell: {
    openclaw: 'ghcr.io/yuan-lab-llm/agentsruntime/openclaw-shell:latest',
    ubuntu: 'ubuntu:22.04',
    webtop: 'ubuntu:22.04',
    hermes: 'ghcr.io/yuan-lab-llm/agentsruntime/hermes-shell:latest',
    debian: 'debian:12',
    centos: 'quay.io/centos/centos:stream9',
    custom: 'registry.example.com/your-custom-shell-image:latest',
  },
};

const getDefaultImage = (instanceType: string, runtimeType: 'desktop' | 'shell') =>
  DEFAULT_IMAGES[runtimeType][instanceType] ?? DEFAULT_IMAGES.desktop.custom;

const getDefaultTitle = (instanceType: string, runtimeType: 'desktop' | 'shell') => {
  const typeLabel = IMAGE_TYPE_OPTIONS.find((option) => option.value === instanceType)?.label ?? instanceType;
  return `${typeLabel} ${runtimeType === 'shell' ? 'Shell' : 'Desktop'}`;
};

interface EditableImageCard extends SystemImageSetting {
  local_id: string;
  isNew?: boolean;
  saving?: boolean;
  error?: string | null;
}

const SystemSettingsPage: React.FC = () => {
  const { t } = useI18n();
  const [cards, setCards] = useState<EditableImageCard[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState<string | null>(null);

  const usedPairs = useMemo(
    () => cards.map((card) => `${card.instance_type}:${card.runtime_type ?? 'desktop'}`).filter(Boolean),
    [cards],
  );

  useEffect(() => {
    const loadSettings = async () => {
      try {
        setLoading(true);
        setPageError(null);
        const items = await systemSettingsService.getImageSettings();
        setCards(items.filter((item) => item.is_enabled !== false).map((item, index) => {
          const runtimeType = item.runtime_type ?? 'desktop';
          return {
            ...item,
            runtime_type: runtimeType,
            local_id: `${item.instance_type}-${runtimeType}-${index}`,
            error: null,
          };
        }));
      } catch (error: any) {
        setPageError(error.response?.data?.error || t('systemSettingsPage.loadFailed'));
      } finally {
        setLoading(false);
      }
    };

    loadSettings();
  }, []);

  const addCard = () => {
    const nextPair = IMAGE_TYPE_OPTIONS
      .flatMap((type) => RUNTIME_TYPE_OPTIONS.map((runtime) => ({
        instanceType: type.value,
        runtimeType: runtime.value,
      })))
      .find((item) => !usedPairs.includes(`${item.instanceType}:${item.runtimeType}`));
    const instanceType = nextPair?.instanceType ?? 'ubuntu';
    const runtimeType = nextPair?.runtimeType ?? 'desktop';
    setCards((current) => [
      ...current,
      {
        local_id: `new-${Date.now()}`,
        instance_type: instanceType,
        runtime_type: runtimeType,
        display_name: getDefaultTitle(instanceType, runtimeType),
        image: getDefaultImage(instanceType, runtimeType),
        isNew: true,
        is_enabled: true,
        error: null,
      },
    ]);
  };

  const updateCard = (localId: string, patch: Partial<EditableImageCard>) => {
    setCards((current) => current.map((card) => {
      if (card.local_id !== localId) {
        return card;
      }

      const next = { ...card, ...patch, error: null };
      if (patch.instance_type || patch.runtime_type) {
        const instanceType = patch.instance_type ?? next.instance_type;
        const runtimeType = patch.runtime_type ?? next.runtime_type ?? 'desktop';
        const previousDefaultImage = getDefaultImage(card.instance_type, card.runtime_type ?? 'desktop');
        next.runtime_type = runtimeType;
        next.display_name = getDefaultTitle(instanceType, runtimeType);
        if (card.isNew && (!card.image || card.image === card.display_name || card.image === previousDefaultImage)) {
          next.image = getDefaultImage(instanceType, runtimeType);
        }
      }
      return next;
    }));
  };

  const saveCard = async (card: EditableImageCard) => {
    if (!card.instance_type || !card.image.trim()) {
      updateCard(card.local_id, { error: t('systemSettingsPage.requiredFields') });
      return;
    }

    const runtimeType = card.runtime_type ?? 'desktop';
    const normalizedImage = card.image.trim().toLowerCase();
    const duplicate = cards.some((item) =>
      item.local_id !== card.local_id &&
      item.instance_type === card.instance_type &&
      (item.runtime_type ?? 'desktop') === runtimeType &&
      item.image.trim().toLowerCase() === normalizedImage,
    );
    if (duplicate) {
      updateCard(card.local_id, { error: t('systemSettingsPage.duplicateImage') });
      return;
    }

    updateCard(card.local_id, { saving: true, error: null });

    try {
      const saved = await systemSettingsService.saveImageSetting({
        id: card.id,
        instance_type: card.instance_type,
        runtime_type: runtimeType,
        display_name: card.display_name,
        image: card.image.trim(),
      });

      setCards((current) => current.map((item) => item.local_id === card.local_id ? {
        ...item,
        ...saved,
        local_id: item.local_id,
        isNew: false,
        saving: false,
        error: null,
      } : item));
    } catch (error: any) {
      updateCard(card.local_id, {
        saving: false,
        error: error.response?.data?.error || t('systemSettingsPage.saveFailed'),
      });
    }
  };

  const deleteCard = async (card: EditableImageCard) => {
    if (card.isNew) {
      setCards((current) => current.filter((item) => item.local_id !== card.local_id));
      return;
    }

    updateCard(card.local_id, { saving: true, error: null });
    try {
      await systemSettingsService.deleteImageSetting(card.id ?? card.instance_type);
      setCards((current) => current.filter((item) => item.local_id !== card.local_id));
    } catch (error: any) {
      updateCard(card.local_id, {
        saving: false,
        error: error.response?.data?.error || t('systemSettingsPage.deleteFailed'),
      });
    }
  };

  return (
    <AdminLayout title={t('admin.systemSettings')}>
      <div className="space-y-6">
        <PasswordSettingsSection />
        <section className="app-panel p-6">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 className="text-xl font-semibold text-gray-900">{t('systemSettingsPage.runtimeImageCards')}</h2>
              <p className="mt-1 text-sm text-gray-500">
                {t('systemSettingsPage.runtimeImageCardsSubtitle')}
              </p>
            </div>
            <button
              type="button"
              onClick={addCard}
              className="app-button-primary"
            >
              {t('systemSettingsPage.addCard')}
            </button>
          </div>

          {pageError && (
            <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              {pageError}
            </div>
          )}

          {loading ? (
            <div className="mt-6 text-sm text-gray-500">{t('common.loading')}</div>
          ) : (
            <div className="mt-6 grid grid-cols-1 gap-4 xl:grid-cols-2">
              {cards.map((card) => {
                const runtimeType = card.runtime_type ?? 'desktop';
                const defaultImage = getDefaultImage(card.instance_type, runtimeType);

                return (
                  <div key={card.local_id} className="rounded-lg border border-[#ead8cf] bg-[rgba(255,248,245,0.84)] p-5 shadow-[0_18px_42px_-34px_rgba(72,44,24,0.42)]">
                    <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
                      <div>
                        <label className="block text-sm font-medium text-gray-700">{t('systemSettingsPage.instanceType')}</label>
                        <select
                          value={card.instance_type}
                          onChange={(event) => updateCard(card.local_id, { instance_type: event.target.value })}
                          className="app-input mt-1 block w-full"
                        >
                          {IMAGE_TYPE_OPTIONS.map((option) => (
                            <option key={option.value} value={option.value}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label className="block text-sm font-medium text-gray-700">{t('systemSettingsPage.runtimeType')}</label>
                        <select
                          value={runtimeType}
                          onChange={(event) => updateCard(card.local_id, { runtime_type: event.target.value as 'desktop' | 'shell' })}
                          className="app-input mt-1 block w-full"
                        >
                          {RUNTIME_TYPE_OPTIONS.map((option) => (
                            <option key={option.value} value={option.value}>
                              {t(option.labelKey)}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label className="block text-sm font-medium text-gray-700">{t('systemSettingsPage.cardTitle')}</label>
                        <input
                          type="text"
                          value={card.display_name}
                          onChange={(event) => updateCard(card.local_id, { display_name: event.target.value })}
                          className="app-input mt-1 block w-full"
                        />
                      </div>
                    </div>

                    <div className="mt-4">
                      <label className="block text-sm font-medium text-gray-700">{t('systemSettingsPage.imageAddress')}</label>
                      <input
                        type="text"
                        value={card.image}
                        onChange={(event) => updateCard(card.local_id, { image: event.target.value })}
                        placeholder={defaultImage}
                        className="app-input mt-1 block w-full"
                      />
                    </div>

                    <p className="mt-3 text-xs text-gray-500">
                      {t('systemSettingsPage.defaultImage')}: <span className="font-mono">{defaultImage}</span>
                    </p>

                    {card.error && (
                      <div className="mt-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
                        {card.error}
                      </div>
                    )}

                    <div className="mt-4 flex items-center justify-end gap-3">
                      <button
                        type="button"
                        onClick={() => deleteCard(card)}
                        disabled={card.saving}
                        className="app-button-secondary disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {t('common.delete')}
                      </button>
                      <button
                        type="button"
                        onClick={() => saveCard(card)}
                        disabled={card.saving}
                        className="app-button-primary disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {card.saving ? t('modelManagementPage.saving') : t('common.save')}
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {!loading && cards.length === 0 && (
            <div className="mt-6 rounded-[24px] border border-dashed border-[#ead8cf] bg-[rgba(255,248,245,0.72)] px-6 py-10 text-center text-sm text-gray-500">
              {t('systemSettingsPage.empty')}
            </div>
          )}
        </section>

      </div>
    </AdminLayout>
  );
};

export default SystemSettingsPage;
