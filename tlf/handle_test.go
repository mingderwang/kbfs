package tlf

import (
	"testing"

	"github.com/keybase/client/go/protocol/keybase1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeHandle(t *testing.T) {
	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	r := []keybase1.UID{
		keybase1.MakeTestUID(5),
		keybase1.MakeTestUID(1),
	}

	uw := []keybase1.SocialAssertion{
		{
			User:    "user2",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service1",
		},
	}

	ur := []keybase1.SocialAssertion{
		{
			User:    "user5",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service2",
		},
	}

	h, err := MakeHandle(w, r, uw, ur, nil)
	require.NoError(t, err)
	require.Equal(t, []keybase1.UID{
		keybase1.MakeTestUID(3),
		keybase1.MakeTestUID(4),
	}, h.Writers)
	require.Equal(t, []keybase1.UID{
		keybase1.MakeTestUID(1),
		keybase1.MakeTestUID(5),
	}, h.Readers)
	require.Equal(t, []keybase1.SocialAssertion{
		{
			User:    "user1",
			Service: "service1",
		},
		{
			User:    "user2",
			Service: "service3",
		},
	}, h.UnresolvedWriters)
	require.Equal(t, []keybase1.SocialAssertion{
		{
			User:    "user1",
			Service: "service2",
		},
		{
			User:    "user5",
			Service: "service3",
		},
	}, h.UnresolvedReaders)
}

func TestMakeHandleFailures(t *testing.T) {
	_, err := MakeHandle(nil, nil, nil, nil, nil)
	assert.Equal(t, errNoWriters, err)

	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	r := []keybase1.UID{
		keybase1.PUBLIC_UID,
		keybase1.MakeTestUID(2),
	}

	_, err = MakeHandle(r, nil, nil, nil, nil)
	assert.Equal(t, errInvalidWriter, err)

	_, err = MakeHandle(w, r, nil, nil, nil)
	assert.Equal(t, errInvalidReader, err)

	ur := []keybase1.SocialAssertion{
		{
			User:    "user5",
			Service: "service3",
		},
	}

	_, err = MakeHandle(w, r[:1], nil, ur, nil)
	assert.Equal(t, errInvalidReader, err)
}

func TestHandleAccessorsPrivate(t *testing.T) {
	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	r := []keybase1.UID{
		keybase1.MakeTestUID(5),
		keybase1.MakeTestUID(1),
	}

	uw := []keybase1.SocialAssertion{
		{
			User:    "user2",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service1",
		},
	}

	ur := []keybase1.SocialAssertion{
		{
			User:    "user5",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service2",
		},
	}

	h, err := MakeHandle(w, r, uw, ur, nil)
	require.NoError(t, err)

	require.False(t, h.IsPublic())

	for _, u := range w {
		require.True(t, h.IsWriter(u))
		require.True(t, h.IsReader(u))
	}

	for _, u := range r {
		require.False(t, h.IsWriter(u))
		require.True(t, h.IsReader(u))
	}

	for i := 6; i < 10; i++ {
		u := keybase1.MakeTestUID(uint32(i))
		require.False(t, h.IsWriter(u))
		require.False(t, h.IsReader(u))
	}

	require.Equal(t, h.ResolvedUsers(),
		[]keybase1.UID{
			keybase1.MakeTestUID(3),
			keybase1.MakeTestUID(4),
			keybase1.MakeTestUID(1),
			keybase1.MakeTestUID(5),
		})
	require.True(t, h.HasUnresolvedUsers())
	require.Equal(t, h.UnresolvedUsers(),
		[]keybase1.SocialAssertion{
			{
				User:    "user1",
				Service: "service1",
			},
			{
				User:    "user2",
				Service: "service3",
			},
			{
				User:    "user1",
				Service: "service2",
			},
			{
				User:    "user5",
				Service: "service3",
			},
		})
}

func TestHandleAccessorsPublic(t *testing.T) {
	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	uw := []keybase1.SocialAssertion{
		{
			User:    "user2",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service1",
		},
	}

	h, err := MakeHandle(
		w, []keybase1.UID{keybase1.PUBLIC_UID}, uw, nil, nil)
	require.NoError(t, err)

	require.True(t, h.IsPublic())

	for _, u := range w {
		require.True(t, h.IsWriter(u))
		require.True(t, h.IsReader(u))
	}

	for i := 6; i < 10; i++ {
		u := keybase1.MakeTestUID(uint32(i))
		require.False(t, h.IsWriter(u))
		require.True(t, h.IsReader(u))
	}

	require.Equal(t, h.ResolvedUsers(),
		[]keybase1.UID{
			keybase1.MakeTestUID(3),
			keybase1.MakeTestUID(4),
		})
	require.True(t, h.HasUnresolvedUsers())
	require.Equal(t, h.UnresolvedUsers(),
		[]keybase1.SocialAssertion{
			{
				User:    "user1",
				Service: "service1",
			},
			{
				User:    "user2",
				Service: "service3",
			},
		})
}

func TestHandleHasUnresolvedUsers(t *testing.T) {
	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	uw := []keybase1.SocialAssertion{
		{
			User:    "user2",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service1",
		},
	}

	ur := []keybase1.SocialAssertion{
		{
			User:    "user5",
			Service: "service3",
		},
		{
			User:    "user1",
			Service: "service2",
		},
	}

	h, err := MakeHandle(w, nil, uw, ur, nil)
	require.NoError(t, err)
	require.True(t, h.HasUnresolvedUsers())

	uw = h.UnresolvedWriters
	h.UnresolvedWriters = nil
	require.True(t, h.HasUnresolvedUsers())

	h.UnresolvedReaders = nil
	require.False(t, h.HasUnresolvedUsers())

	h.UnresolvedWriters = uw
	require.True(t, h.HasUnresolvedUsers())
}

func TestHandleResolveAssertions(t *testing.T) {
	w := []keybase1.UID{
		keybase1.MakeTestUID(4),
		keybase1.MakeTestUID(3),
	}

	r := []keybase1.UID{
		keybase1.MakeTestUID(5),
		keybase1.MakeTestUID(1),
	}

	uw := []keybase1.SocialAssertion{
		{
			User:    "user2",
			Service: "service3",
		},
		{
			User:    "user7",
			Service: "service2",
		},
		{
			User:    "user1",
			Service: "service1",
		},
	}

	ur := []keybase1.SocialAssertion{
		{
			User:    "user6",
			Service: "service3",
		},
		{
			User:    "user8",
			Service: "service1",
		},
		{
			User:    "user5",
			Service: "service1",
		},
		{
			User:    "user1",
			Service: "service2",
		},
		{
			User:    "user9",
			Service: "service1",
		},
		{
			User:    "user9",
			Service: "service3",
		},
	}

	h, err := MakeHandle(w, r, uw, ur, nil)
	require.NoError(t, err)

	assertions := make(map[keybase1.SocialAssertion]keybase1.UID)
	assertions[uw[0]] = keybase1.MakeTestUID(2) // new writer
	assertions[uw[2]] = keybase1.MakeTestUID(1) // reader promoted to writer
	assertions[ur[0]] = keybase1.MakeTestUID(6) // new reader
	assertions[ur[2]] = keybase1.MakeTestUID(5) // already a reader
	assertions[ur[3]] = keybase1.MakeTestUID(1) // already a writer
	assertions[ur[4]] = keybase1.MakeTestUID(9) // new reader
	assertions[ur[5]] = keybase1.MakeTestUID(9) // already a reader

	h = h.ResolveAssertions(assertions)

	require.Equal(t, []keybase1.UID{
		keybase1.MakeTestUID(1),
		keybase1.MakeTestUID(2),
		keybase1.MakeTestUID(3),
		keybase1.MakeTestUID(4),
	}, h.Writers)
	require.Equal(t, []keybase1.UID{
		keybase1.MakeTestUID(5),
		keybase1.MakeTestUID(6),
		keybase1.MakeTestUID(9),
	}, h.Readers)
	require.Equal(t, []keybase1.SocialAssertion{
		{
			User:    "user7",
			Service: "service2",
		},
	}, h.UnresolvedWriters)
	require.Equal(t, []keybase1.SocialAssertion{
		{
			User:    "user8",
			Service: "service1",
		},
	}, h.UnresolvedReaders)
}
