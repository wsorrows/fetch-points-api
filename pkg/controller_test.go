package rewards

import (
    "fmt"
    "testing"
    "net/http"
    "net"
    "io/ioutil"
    "io"
    "bytes"
    "encoding/json"
    "time"
    "errors"
)

func sendPostRequest(url string, bytes io.Reader) (JSONResponse, error) {
    var j JSONResponse
    resp, err := http.Post(url, "application/json", bytes)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Error making request: %s", err)) 
    }

    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Failed to read response body: %s", err))
    }
    
    err = json.Unmarshal(body, &j)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Failed to parse request json(%s)", body))
    }

    return j, nil
}

func sendGetRequest(url string) (JSONResponse, error) {
    var j JSONResponse
    resp, err := http.Get(url)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Error making request: %s", err)) 
    }

    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Failed to read response body: %s", err))
    }
    
    err = json.Unmarshal(body, &j)
    if err != nil {
        return j, errors.New(fmt.Sprintf("Failed to parse request json(%s)", body))
    }

    return j, nil
}

func startServer(t *testing.T, port int, lock chan struct{}) *http.Server {
    api := NewRewardAPI()

    server := &http.Server{
        Addr: fmt.Sprintf(":%d", port),
        Handler: api,
    }

    go func() {
        l, err := net.Listen("tcp", server.Addr)
        lock <- struct{}{} // Signal that server is started
        if err != nil {
            t.Errorf("Error listing to tcp socket: %s", err)
            return
        }

        if err := server.Serve(l); err != nil && err != http.ErrServerClosed {
            t.Errorf("Server failed to start: %s", err)
        }
        lock <- struct{}{} //Signal that server is closed
    }()

    return server
}

func TestAddHandler(t *testing.T) {
    lock := make(chan struct{}, 1)
    server := startServer(t, 8080, lock)

    <- lock // Wait for server to start

    type ResponseFunc func(JSONResponse)
    testCases := []struct {
        addJSON []byte
        Validate ResponseFunc
    }{
        {
            []byte(`[{"timestamp": "2020-11-15T00:00:00Z", "payer": "PAYER_A", "points": 150}]`),
            func(j JSONResponse) {
                if !j.Success {
                    t.Errorf("Failed to add reward as expected: %s", j.Error)
                }
            },
        },
        {
            []byte(`[{"timestamp": "nonsense", "payer": "PAYER_A", "points": 150}]`),
            func(j JSONResponse) {
                if j.Success || j.Error != "Invalid timestamp" {
                    t.Errorf("Added reward when failure expected")
                }
            },
        },
        {
            []byte(`[{"timestamp": "2020-11-15T00:20:00Z", "payer": "PAYER_A"}]`),
            func(j JSONResponse) {
                if j.Success || j.Error != "Missing required parameters" {
                    t.Errorf("Added reward when failure expected")
                }
            },
        },
    }

    for _, testCase := range testCases {
        j, err := sendPostRequest("http://localhost:8080/add-points", bytes.NewBuffer(testCase.addJSON))
        if err != nil {
            t.Fatalf("Error making post request: %s", err)
        }

        testCase.Validate(j)
    }


    server.Close()
    <- lock // Wait for server to close
}

func TestBalanceHandler(t *testing.T) {
    lock := make(chan struct{}, 1)
    server := startServer(t, 8080, lock)

    <- lock // Wait for server to start
    addRewards := func() {
        ts := time.Now()
        rewards := []Reward{
            {ts, 100, "PAYER_A"},
            {ts.Add(time.Duration(10) * time.Second), 200, "PAYER_B"},
            {ts.Add(time.Duration(20) * time.Second), 200, "PAYER_C"},
            {ts.Add(time.Duration(30) * time.Second), 100, "PAYER_B"},
            {ts.Add(time.Duration(40) * time.Second), 200, "PAYER_C"},
        }

        var buff bytes.Buffer
        err := json.NewEncoder(&buff).Encode(rewards)
        if err != nil {
            t.Fatalf("Error encoding json: %s", err)
        }

        _, err = sendPostRequest("http://localhost:8080/add-points", &buff)
        if err != nil {
            t.Errorf("Error making post request: %s", err)
        }
    }

    type SetupFunc func()
    type ResponseFunc func(JSONResponse)

    testCases := []struct {
        Setup SetupFunc
        Validate ResponseFunc
    }{
        {
            func(){}, 
            func(j JSONResponse) {
                if !j.Success || j.Error != nil || j.Data == nil {
                    t.Errorf("Expected success, but got failure: %s", j.Error)
                }

                data := j.Data.(map[string]interface{})
                if len(data) != 0 {
                    t.Errorf("Expected 0 payer balances; got %d", len(data))
                }
            },
        },
        {
            addRewards, 
            func(j JSONResponse) {
                if !j.Success || j.Error != nil || j.Data == nil {
                    t.Errorf("Expected success, but got failure: %s", j.Error)
                }

                data := j.Data.(map[string]interface{})
                if len(data) != 3 {
                    t.Errorf("Expected 3 payer balances; got %d", len(data))
                }

                expectations := []struct{
                    payer string 
                    points int64
                }{{"PAYER_A", 100},{"PAYER_B", 300},{"PAYER_C", 400}}

                for _, expectation := range expectations {
                    if points, ok := data[expectation.payer]; ok {
                        if int64(points.(float64)) != expectation.points {
                            t.Errorf("Expected balance of %d points for %s; got %d", expectation.points, expectation.payer, int64(points.(float64)))
                        }
                    } else {
                        t.Errorf("Expected balance for %s; found none", expectation.payer)
                    }
                }
            },
        },
    }

    for _, testCase := range testCases {
        testCase.Setup()

        j, err := sendGetRequest("http://localhost:8080/check-balance")
        if err != nil {
            t.Errorf("Error making post request: %s", err)
        }

        testCase.Validate(j)
    }



    server.Close()
    <- lock // Wait for server to close
}


func TestUseHandler(t *testing.T) {
    lock := make(chan struct{}, 1)
    server := startServer(t, 8080, lock)

    <- lock // Wait for server to start
    rewards := []byte (`[
        { "payer": "UNILEVER", "points": 200, "timestamp": "2020-10-31T11:00:00Z" },
        { "payer": "DANNON", "points": -200, "timestamp": "2020-10-31T15:00:00Z" },
        { "payer": "DANNON", "points": -100, "timestamp": "2020-11-01T14:20:00Z" },
        { "payer": "MILLER COORS", "points": 10000, "timestamp": "2020-11-01T14:00:00Z" },
        { "payer": "DANNON", "points": 300, "timestamp": "2020-10-31T10:00:00Z" }
    ]`)

    _, err := sendPostRequest("http://localhost:8080/add-points", bytes.NewBuffer(rewards))
    if err != nil {
        t.Errorf("Error making post request: %s", err)
    }

    type ResponseFunc func(JSONResponse)

    testCases := []struct {
        UseJSON []byte
        Validate ResponseFunc
    }{
        {[]byte(`{"points": 5000}`), func(j JSONResponse) {
            if !j.Success || j.Error != nil || j.Data == nil {
                t.Errorf("Expected success, but got failure: %s", j.Error)
            }

            data := j.Data.(map[string]interface{})
            if len(data) != 2 {
                t.Errorf("Expected 2 payer deductions; got %d", len(data))
            }
        }},
        {[]byte(`{"points": 10000}`), func(j JSONResponse) {
            if j.Success || j.Error != "Not enough points" {
                t.Errorf("Expected failure, but got success")
            }
        }},
        {[]byte(`{}`), func(j JSONResponse) {
            if j.Success || j.Error != "Missing required parameters" {
                t.Errorf("Expected failure, but got success")
            }
        }},
    }

    for _, testCase := range testCases {
        j, err := sendPostRequest("http://localhost:8080/use-points", bytes.NewBuffer(testCase.UseJSON))
        if err != nil {
            t.Errorf("Error making post request: %s", err)
        }

        testCase.Validate(j)
    }



    server.Close()
    <- lock // Wait for server to close
}


