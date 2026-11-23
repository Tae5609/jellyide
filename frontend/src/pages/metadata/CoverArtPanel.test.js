import React from 'react';
import { render, screen } from '@testing-library/react';
import CoverArtPanel from './CoverArtPanel';

function renderPanel(lookupCoverUrl) {
  render(
    <CoverArtPanel
      albumId="a1"
      coverKey={0}
      lookupCoverUrl={lookupCoverUrl}
      onDone={jest.fn()}
      onError={jest.fn()}
    />,
  );
}

describe('CoverArtPanel', () => {
  test('Use from lookup is disabled until a lookup applied with the cover option', () => {
    renderPanel('');
    const btn = screen.getByRole('button', { name: /Use from lookup/ });
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute(
      'title',
      'Apply a release in the MusicBrainz lookup with "Set cover art from release" checked first',
    );
  });

  test('Use from lookup retries the release cover once a lookup set it', () => {
    renderPanel('https://coverartarchive.org/release/m1/front');
    const btn = screen.getByRole('button', { name: /Use from lookup/ });
    expect(btn).toBeEnabled();
    expect(btn).toHaveAttribute(
      'title',
      'Re-download the cover from the selected MusicBrainz release',
    );
  });
});
