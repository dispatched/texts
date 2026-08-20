import { useState, useEffect } from 'react'
import axios from 'axios'

const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8085/api'

const MESSAGE_LIMIT_OPTIONS = [
  { value: 100, label: '100' },
  { value: 1000, label: '1,000' },
  { value: 10000, label: '10,000' },
  { value: 100000, label: '100,000' },
  { value: 200000, label: '200,000' },
  { value: 500000, label: '500,000' },
]

function SettingsModal({ show, onClose, onSettingsUpdated }) {
  const [settings, setSettings] = useState({
    conversations: {
      show_calls: true,
      message_limit: 100000
    }
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
	const [clearing, setClearing] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (show) {
      fetchSettings()
    }
  }, [show])

  const fetchSettings = async () => {
    try {
      setLoading(true)
      const response = await axios.get(`${API_BASE}/settings`)
      setSettings(response.data)
    } catch (err) {
      console.error('Failed to fetch settings:', err)
      setError('Failed to load settings')
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async () => {
    try {
      setSaving(true)
      setError('')
      await axios.put(`${API_BASE}/settings`, settings)

      if (onSettingsUpdated) {
        onSettingsUpdated(settings)
      }

      onClose()
    } catch (err) {
      console.error('Failed to save settings:', err)
      setError('Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  const handleToggleShowCalls = () => {
    setSettings({
      ...settings,
      conversations: {
        ...settings.conversations,
        show_calls: !settings.conversations.show_calls
      }
    })
  }

  const handleMessageLimitChange = (e) => {
    setSettings({
      ...settings,
      conversations: {
        ...settings.conversations,
        message_limit: parseInt(e.target.value, 10)
      }
    })
  }

  const handleClearImportedData = async () => {
	const confirmed = window.confirm(
	  'Permanently delete all imported messages, calls, and media? Your account and settings will be kept.'
	)
	if (!confirmed) return

	try {
	  setClearing(true)
	  setError('')
	  await axios.delete(`${API_BASE}/data`)
	  window.location.reload()
	} catch (err) {
	  console.error('Failed to clear imported data:', err)
	  setError('Failed to clear imported data')
	  setClearing(false)
	}
  }

  if (!show) return null

  return (
    <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
      <div className="modal-dialog modal-dialog-centered">
        <div className="modal-content">
          <div className="modal-header">
            <h5 className="modal-title">Settings</h5>
            <button type="button" className="btn-close" onClick={onClose} disabled={saving}></button>
          </div>
          <div className="modal-body">
            {loading ? (
              <div className="text-center py-4">
                <div className="spinner-border" role="status">
                  <span className="visually-hidden">Loading...</span>
                </div>
              </div>
            ) : (
              <>
                {error && (
                  <div className="alert alert-danger" role="alert">
                    {error}
                  </div>
                )}

                <h6 className="mb-3">Conversations</h6>

                <div className="form-check form-switch mb-3">
                  <input
                    className="form-check-input"
                    type="checkbox"
                    id="showCallsToggle"
                    checked={settings.conversations.show_calls}
                    onChange={handleToggleShowCalls}
                    disabled={saving}
                  />
                  <label className="form-check-label" htmlFor="showCallsToggle">
                    Show calls in conversation list
                  </label>
                  <div className="form-text">
                    When enabled, phone calls will appear in the conversation list alongside messages.
                  </div>
                </div>

                <div className="mb-3">
                  <label htmlFor="messageLimitSelect" className="form-label">Messages to load</label>
                  <select
                    className="form-select"
                    id="messageLimitSelect"
                    value={settings.conversations.message_limit || 100000}
                    onChange={handleMessageLimitChange}
                    disabled={saving}
                  >
                    {MESSAGE_LIMIT_OPTIONS.map(opt => (
                      <option key={opt.value} value={opt.value}>{opt.label}</option>
                    ))}
                  </select>
                  <div className="form-text">
                    Maximum number of messages shown when opening a conversation.
                  </div>
                </div>

                <hr className="my-4" />

                <h6 className="mb-3">Export</h6>
                <div className="d-grid gap-2">
                  <a
                    href={`${API_BASE}/export/media`}
                    className="btn btn-outline-primary"
                  >
                    Download Media by Conversation (.zip)
                  </a>
                  <a
                    href={`${API_BASE}/export/media?view=all`}
                    className="btn btn-outline-primary"
                  >
                    Download All Media Together (.zip)
                  </a>
                </div>
                <div className="form-text">
                  Choose the existing contact folders or one combined view containing every photo, video, and audio attachment.
                </div>

				<hr className="my-4" />

				<h6 className="mb-3 text-danger">Imported Data</h6>
				<button
				  type="button"
				  className="btn btn-outline-danger w-100"
				  onClick={handleClearImportedData}
				  disabled={clearing}
				>
				  {clearing ? 'Clearing Imported Data...' : 'Clear Imported Backup'}
				</button>
				<div className="form-text">
				  Permanently removes imported messages, calls, and media while keeping your account and settings.
				</div>
              </>
            )}
          </div>
          <div className="modal-footer">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={onClose}
			  disabled={saving || clearing}
            >
              Cancel
            </button>
            <button
              type="button"
              className="btn btn-primary"
              onClick={handleSave}
			  disabled={loading || saving || clearing}
            >
              {saving ? (
                <>
                  <span className="spinner-border spinner-border-sm me-2" role="status" aria-hidden="true"></span>
                  Saving...
                </>
              ) : (
                'Save Changes'
              )}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

export default SettingsModal
