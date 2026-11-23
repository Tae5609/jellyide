import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import BulkEditPanel from './BulkEditPanel';

function renderPanel(overrides = {}) {
  const onApply = jest.fn();
  const onError = jest.fn();
  render(<BulkEditPanel selectedCount={2} onApply={onApply} onError={onError} {...overrides} />);
  return { onApply, onError };
}

describe('BulkEditPanel', () => {
  test('padded text values are staged trimmed', async () => {
    const user = userEvent.setup();
    const { onApply } = renderPanel();

    await user.selectOptions(screen.getByLabelText('Field'), 'Genre');
    // Literal spaces: user-event types them, {Space} tokens are no-ops.
    await user.type(screen.getByLabelText('Value'), '  Jazz  ');
    await user.click(screen.getByRole('button', { name: 'Apply to selection' }));

    // The backend stores text verbatim (apply.go), so the panel must stage
    // the trimmed value, not the padded draft.
    expect(onApply).toHaveBeenCalledWith('genre', 'Jazz');
  });

  test('padded values on the default field are trimmed too', async () => {
    const user = userEvent.setup();
    const { onApply } = renderPanel();

    await user.type(screen.getByLabelText('Value'), '  One  ');
    await user.click(screen.getByRole('button', { name: 'Apply to selection' }));

    expect(onApply).toHaveBeenCalledWith('title', 'One');
  });

  test('an empty value clears the tag (null)', async () => {
    const user = userEvent.setup();
    const { onApply } = renderPanel();

    await user.selectOptions(screen.getByLabelText('Field'), 'Genre');
    await user.click(screen.getByRole('button', { name: 'Apply to selection' }));

    expect(onApply).toHaveBeenCalledWith('genre', null);
  });

  test('padded int values are validated trimmed', async () => {
    const user = userEvent.setup();
    const { onApply } = renderPanel();

    await user.selectOptions(screen.getByLabelText('Field'), '#');
    await user.type(screen.getByLabelText('Value'), '  3  ');
    await user.click(screen.getByRole('button', { name: 'Apply to selection' }));

    expect(onApply).toHaveBeenCalledWith('track_number', 3);
  });
});
