import React, { createContext, useContext, useState, useEffect } from 'react';

const ModalContext = createContext();

export function ModalProvider({ children }) {
  const [modal, setModal] = useState(null);

  // Esc to close support
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape' && modal) {
        if (modal.type === 'confirm' && modal.onCancel) modal.onCancel();
        else if (modal.type === 'alert' && modal.onConfirm) modal.onConfirm();
        setModal(null);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [modal]);

  const showAlert = (title, message) => {
    return new Promise((resolve) => {
      setModal({
        type: 'alert',
        title,
        message,
        onConfirm: () => {
          setModal(null);
          resolve(true);
        },
      });
    });
  };

  const showConfirm = (title, message) => {
    return new Promise((resolve) => {
      setModal({
        type: 'confirm',
        title,
        message,
        onConfirm: () => {
          setModal(null);
          resolve(true);
        },
        onCancel: () => {
          setModal(null);
          resolve(false);
        },
      });
    });
  };

  // isOpen lets pages pause global keyboard shortcuts while a modal is up.
  return (
    <ModalContext.Provider value={{ showAlert, showConfirm, isOpen: modal !== null }}>
      {children}
      {modal && (
        <div className="modal-backdrop">
          <div className="modal-content fade-in-up">
            <h3 className="modal-title">{modal.title}</h3>
            <p className="modal-message">{modal.message}</p>
            <div className="modal-actions">
              {modal.type === 'confirm' && (
                <button className="btn-secondary" onClick={modal.onCancel}>
                  Cancel
                </button>
              )}
              <button className="btn-primary" onClick={modal.onConfirm}>
                {modal.type === 'confirm' ? 'Confirm' : 'OK'}
              </button>
            </div>
          </div>
        </div>
      )}
    </ModalContext.Provider>
  );
}

export const useModal = () => useContext(ModalContext);
