package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
)

const raceAttempts = 24

func fromIP(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/manage/login", nil)
	r.Header.Set("X-Forwarded-For", ip)
	return r
}

func concurrently(n int, fn func(i int) error) map[string]int {
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			err := fn(i)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				got["ok"]++
			case errors.Is(err, ErrRefused):
				got["refused"]++
			case errors.Is(err, ErrRateLimited):
				got["limited"]++
			case errors.Is(err, ErrUnauthorized):
				got["unauthorized"]++
			default:
				got[err.Error()]++
			}
		}(i)
	}
	close(start)
	wg.Wait()
	return got
}

func (s *Service) counted(t *testing.T, k store.FailureKey) (int, int) {
	t.Helper()
	since := s.Now().Add(-loginWindow)
	n, err := s.Store.LoginFailures(k, since)
	if err != nil {
		t.Fatal(err)
	}
	total, err := s.Store.NameFailures(k.Step, k.Name, since)
	if err != nil {
		t.Fatal(err)
	}
	return n, total
}

func TestConcurrentWrongPasswordsHoldTheSourceBudget(t *testing.T) {
	s, _ := boundService(t)
	s.TrustedProxy = netip.MustParseAddr("192.0.2.1")
	got := concurrently(raceAttempts, func(int) error {
		_, err := s.StartLogin(httptest.NewRecorder(), fromIP("203.0.113.7"), "ops", "wrong password!!")
		return err
	})
	n, _ := s.counted(t, store.FailureKey{Step: "pw", Source: "203.0.113.7", Name: "ops"})
	if n != loginMaxFailures || got["refused"] != loginMaxFailures || got["limited"] != raceAttempts-loginMaxFailures {
		t.Fatalf("24 concurrent wrong passwords: %d failures recorded, outcomes %v; want 5 refused, 19 limited", n, got)
	}
}

func TestConcurrentWrongPasswordsHoldTheNameCeiling(t *testing.T) {
	s, _ := boundService(t)
	s.TrustedProxy = netip.MustParseAddr("192.0.2.1")
	for i := 0; i < loginNameCeiling-loginMaxFailures; i++ {
		k := store.FailureKey{Step: "pw", Source: fmt.Sprintf("198.51.100.%d", i/loginMaxFailures), Name: "ops"}
		if err := s.Store.RecordLoginFailure(k, s.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got := concurrently(raceAttempts, func(i int) error {
		_, err := s.StartLogin(httptest.NewRecorder(), fromIP(fmt.Sprintf("203.0.113.%d", i+1)), "ops", "wrong password!!")
		return err
	})
	_, total := s.counted(t, store.FailureKey{Step: "pw", Name: "ops"})
	if total != loginNameCeiling || got["refused"] != loginMaxFailures || got["limited"] != raceAttempts-loginMaxFailures {
		t.Fatalf("24 concurrent sources at 45/50: %d failures for the name, outcomes %v", total, got)
	}
}

func TestConcurrentWrongCodesHoldTheBudget(t *testing.T) {
	s, now := boundService(t)
	var pending [][]*http.Cookie
	for i := 0; i < raceAttempts; i++ {
		rec := httptest.NewRecorder()
		if _, err := s.StartLogin(rec, fromIP("192.0.2.1"), "ops", boundPassword); err != nil {
			t.Fatal(err)
		}
		pending = append(pending, rec.Result().Cookies())
	}
	admin, _ := s.Store.Admin("ops")
	secret, _ := s.Sealer.Open(admin.TOTPSecretEnc)
	right, _ := totp.Code(string(secret), *now)
	wrong := []byte(right)
	wrong[0] = '0' + (wrong[0]-'0'+5)%10
	got := concurrently(raceAttempts, func(i int) error {
		r := httptest.NewRequest(http.MethodPost, "/manage/login/totp", nil)
		for _, c := range pending[i] {
			r.AddCookie(c)
		}
		return s.CompleteTOTP(httptest.NewRecorder(), r, string(wrong))
	})
	n, _ := s.counted(t, store.FailureKey{Step: "totp", Source: "192.0.2.1", Name: "ops"})
	if n != loginMaxFailures || got["refused"] != loginMaxFailures || got["limited"] != raceAttempts-loginMaxFailures {
		t.Fatalf("24 concurrent wrong codes: %d failures recorded, outcomes %v", n, got)
	}
}

func TestSuccessClearsOnlyItsReservation(t *testing.T) {
	s, _ := boundService(t)
	k := store.FailureKey{Step: "pw", Source: "192.0.2.1", Name: "ops"}
	for i := 0; i < 3; i++ {
		if err := s.Store.RecordLoginFailure(k, s.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.StartLogin(httptest.NewRecorder(), fromIP("192.0.2.1"), "ops", boundPassword); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.counted(t, k); n != 3 {
		t.Fatalf("after a success the source holds %d failures, want the 3 it had", n)
	}
	if _, err := s.StartLogin(httptest.NewRecorder(), fromIP("192.0.2.1"), "ops", "wrong password!!"); !errors.Is(err, ErrRefused) {
		t.Fatal(err)
	}
	if n, _ := s.counted(t, k); n != 4 {
		t.Fatalf("control: a failure after the success leaves %d, want 4", n)
	}
}
